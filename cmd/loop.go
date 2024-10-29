package main

import (
	"context"
	"sync"

	"github.com/cloudflare/cloudflare-go"
	"github.com/goodieshq/cfdns/config"
	"github.com/rs/zerolog/log"
)

var (
	currentIPv4 = ""
	currentIPv6 = ""
)

func loop(ctx context.Context, api *cloudflare.API, cfg *config.Config) {
	defer func() {
		value := recover()
		if value != nil {
			log.Error().Any("recovered", value).Send()
		}
	}()

	if cfg.IPv4 {
		ipv4, err := GetPublicIPv4()
		if err != nil {
			log.Panic().Err(err).Msg("failed to get public ipv4")
		}

		if ipv4 != currentIPv4 {
			currentIPv4 = ipv4
			log.Info().Msgf("updated ipv4 address: %s", ipv4)
		}

		var wg sync.WaitGroup
		for _, domain := range cfg.Domains {
			wg.Add(1)
			go func() {
				CheckAndUpdateIPv4(ctx, api, cfg, &domain)
				wg.Done()
			}()
		}
		wg.Wait()
	}

	if cfg.IPv6 {
		ipv6, err := GetPublicIPv6()
		if err != nil {
			log.Panic().Err(err).Msg("failed to get public ipv6")
		}

		if ipv6 != currentIPv6 {
			currentIPv6 = ipv6
			log.Info().Msgf("updated ipv6 address: %s", ipv6)
		}

		var wg sync.WaitGroup
		for _, domain := range cfg.Domains {
			wg.Add(1)
			go func() {
				CheckAndUpdateIPv6(ctx, api, cfg, &domain)
				wg.Done()
			}()
		}
		wg.Wait()
	}
}
