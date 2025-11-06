package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/goodieshq/cfdns/pkg/cf"
	"github.com/goodieshq/cfdns/pkg/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const VERSION = "0.2.0"
const DEFAULT_CONFIG_FILE = "cfdns.yaml"

type CLIFlags struct {
	ConfigFile string
}

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

func main() {
	log.Info().Str("version", VERSION).Msg("starting cfdns...")
	info := cli()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// load the configuration file from JSON or YAML
	cfg, err := config.LoadConfig(info.ConfigFile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config file")
	}

	var cfopts []cloudflare.Option

	// set the log output verbosity level
	if cfg.Verbose {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	}

	cfdns, err := cf.NewCFDNS(*cfg, cfopts...)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create cfdns instance")
	}

	// check if the provided zone ID is valid for the scoped token
	valid, err := cfdns.ZoneIsValid(ctx)
	if !valid {
		evt := log.Fatal()
		if err != nil {
			evt = evt.Err(err)
		}
		evt.Msg("failed to validate the zone")
	}

	watcher := watchFile(ctx, info.ConfigFile)

	for {
		log.Info().Msg("starting cfdns processing cycle...")
		cfdns.Process(ctx)
		cfdns.Wait()
		select {
		case <-ctx.Done():
			log.Info().Msg("shutting down cfdns...")
			cfdns.Close()
			log.Info().Msg("cfdns stopped")
			return
		case <-time.After(cfg.Frequency):
		case <-watcher:
			// check file modification time and reload if changed
			log.Info().Msg("configuration file changed, reloading...")
			cfg, err = config.LoadConfig(info.ConfigFile)
			if err != nil {
				log.Error().Err(err).Msg("failed to reload config file, keeping existing configuration")
				continue
			}
			if cfg.Verbose {
				zerolog.SetGlobalLevel(zerolog.InfoLevel)
			} else {
				zerolog.SetGlobalLevel(zerolog.ErrorLevel)
			}
			cfdns.SetConfig(cfg)
		}
	}
}

func cli() *CLIFlags {
	var cliFlags CLIFlags
	flag.StringVar(&cliFlags.ConfigFile, "config", "", "Configuration File")
	flag.StringVar(&cliFlags.ConfigFile, "c", "", "Configuration File (alias)")
	flag.Parse()

	if cliFlags.ConfigFile == "" {
		cliFlags.ConfigFile = DEFAULT_CONFIG_FILE
	}

	return &cliFlags
}

func getLastModTime(filename string) time.Time {
	info, err := os.Stat(filename)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func watchFile(ctx context.Context, filename string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	interval := 1 * time.Second

	go func() {
		modTime := getLastModTime(filename)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
				// get the last modification time of the file
				newModTime := getLastModTime(filename)

				// if we couldn't get the mod time, skip this iteration
				if newModTime.IsZero() {
					continue
				}

				// if this is the first time checking, just set the modTime
				if modTime.IsZero() {
					modTime = newModTime
					continue
				}

				// if the modification time has changed, notify via channel
				if newModTime.After(modTime) {
					modTime = newModTime
					<-time.After(50 * time.Millisecond)
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return ch
}
