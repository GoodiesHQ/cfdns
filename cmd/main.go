package main

import (
	"context"
	"os"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/goodieshq/cfdns/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const VERSION = "0.1"

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	log.Info().Str("version", VERSION).Msg("starting cfdns...")

	info := cli()

	// load the configuration file from JSON or YAML
	cfg, err := config.LoadConfig(info.ConfigFile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config file")
	}

	var cfopts []cloudflare.Option

	// set the log output verbosity level
	if cfg.Verbose {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		cfopts = append(cfopts, cloudflare.UsingLogger(&log.Logger))
	} else {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	}

	// create a new cloudflare API client from the scoped token
	api, err := cloudflare.NewWithAPIToken(cfg.Token, cfopts...)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create the cloudflare api client")
	}

	// create a background context
	ctx := context.Background()

	// check if the provided zone ID is valid for the scoped token
	valid, err := zoneIsValid(ctx, api, cfg.ZoneID)
	if !valid {
		evt := log.Fatal()
		if err != nil {
			evt = evt.Err(err)
		}
		evt.Msg("failed to validate the zone")
	}

	for {
		loop(ctx, api, cfg)
		time.Sleep(cfg.Frequency)
	}
}
