package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goodieshq/cfdns/pkg/cf"
	"github.com/goodieshq/cfdns/pkg/config"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const VERSION = "0.3.1"

type CLIFlags struct {
	ConfigFile string
}

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	godotenv.Load()
}

func main() {
	log.Info().Str("version", VERSION).Msg("Starting CFDNS...")
	info := cli()

	// create a context that is cancelled on SIGINT or SIGTERM
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// load the initial configuration from the YAML file
	cfg, err := config.LoadConfig(info.ConfigFile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config file")
	}

	// set the initial logging level based on config
	if cfg.Verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Create the cfdns instance
	cfdns, err := cf.NewCFDNS(*cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create cfdns instance")
	}

	// create a file watcher for the config file to signal changes
	watcher := watchFile(ctx, info.ConfigFile)

	for {
		// create a timer for the processing frequency
		timer := time.NewTimer(cfg.Frequency)
		stop := func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		tStart := time.Now()
		log.Debug().Msg("Starting CFDNS processing cycle.")
		cfdns.Process(ctx)
		cfdns.Wait()
		log.Info().Str("duration", Dur(time.Since(tStart))).Msg("Completed CFDNS processing cycle.")

		// wait for the next cycle, config file modification, or shutdown signal
		select {
		case <-ctx.Done():
			stop()
			log.Warn().Msg("Shutting down CFDNS...")
			cfdns.Close()
			log.Info().Msg("CFDNS stopped. Exiting.")
			return
		case <-timer.C:
			// continue to next processing cycle
			continue
		case _, ok := <-watcher:
			stop()
			if !ok {
				watcher = nil
				continue
			}

			// check file modification time and reload if changed
			log.Info().Msg("Configuration file changed, reloading...")

			// load the new configuration from file
			cfgNew, err := config.LoadConfig(info.ConfigFile)
			if err != nil {
				log.Error().Err(err).Msg("failed to reload config file, keeping existing configuration")
				continue
			}

			// set the new configuration
			if err := cfdns.SetConfig(cfgNew); err != nil {
				log.Error().Err(err).Msg("failed to apply new configuration, keeping existing configuration")
				continue
			}

			// update logging level based on new config
			if cfg.Verbose {
				zerolog.SetGlobalLevel(zerolog.DebugLevel)
			} else {
				zerolog.SetGlobalLevel(zerolog.InfoLevel)
			}
			log.Info().Msg("Configuration reloaded successfully.")
		}
	}
}

func cli() *CLIFlags {
	var cliFlags CLIFlags
	flag.StringVar(&cliFlags.ConfigFile, "config", "", "Configuration File")
	flag.StringVar(&cliFlags.ConfigFile, "c", "", "Configuration File (alias)")
	flag.Parse()

	if cliFlags.ConfigFile == "" {
		log.Fatal().Msg("configuration file is required, use -config/-c to specify the file")
	}

	return &cliFlags
}

type FileSig struct {
	ModTime time.Time
	Size    int64
	Inode   uint64
}

func (fs1 *FileSig) Changed(fs2 *FileSig) bool {
	if fs1 == nil || fs2 == nil {
		return false
	}
	return fs2.ModTime.After(fs1.ModTime) ||
		fs2.Size != fs1.Size ||
		fs2.Inode != fs1.Inode
}

func getFileSig(filename string) *FileSig {
	info, err := os.Stat(filename)
	if err != nil {
		return nil
	}

	var inode uint64
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		inode = st.Ino
	}

	return &FileSig{
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Inode:   inode,
	}
}

func watchFile(ctx context.Context, filename string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	interval := 1 * time.Second

	go func() {
		defer close(ch)

		fileSig := getFileSig(filename)

		t := time.NewTicker(interval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// get the metadata signature of the file
				fileSigNew := getFileSig(filename)

				// if we couldn't get the mod time, skip this iteration
				if fileSigNew == nil {
					continue
				}

				// if this is the first time checking, just set the modTime
				if fileSig == nil {
					fileSig = fileSigNew
					continue
				}

				if fileSig.Changed(fileSigNew) {
					// update the stored signature and notify
					fileSig = fileSigNew
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

func Dur(d time.Duration) string {
	const precision = 2

	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		f := "%." + fmt.Sprintf("%d", precision) + "fµs"
		return fmt.Sprintf(f, float64(d.Nanoseconds())/1000)
	case d < time.Second:
		f := "%." + fmt.Sprintf("%d", precision) + "fms"
		return fmt.Sprintf(f, float64(d.Microseconds())/1000)
	case d < time.Minute:
		f := "%." + fmt.Sprintf("%d", precision) + "fs"
		return fmt.Sprintf(f, d.Seconds())
	case d < time.Hour:
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
}
