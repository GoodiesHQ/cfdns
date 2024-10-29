package main

import (
	"context"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/goodieshq/cfdns/config"
	"github.com/rs/zerolog/log"
)

const (
	RECORD_TYPE_IPV4 = "A"
	RECORD_TYPE_IPV6 = "AAAA"
)

func getRecords(ctx context.Context, api *cloudflare.API, zoneID, hostname, recordType string) ([]cloudflare.DNSRecord, error) {
	records, _, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{
		Name: hostname,
		Type: recordType,
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func checkAndUpdate(
	ctx context.Context,
	api *cloudflare.API,
	cfg *config.Config,
	domain *config.Domain,
	recordType,
	address string,
) error {
	const timeout = time.Second * 5
	var ctxTimeout context.Context
	var cancel context.CancelFunc

	ctxTimeout, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	records, err := getRecords(ctxTimeout, api, cfg.ZoneID, domain.Hostname, recordType)
	if err != nil {
		return err
	}

	// no records found for this hostname, create a new one
	if len(records) == 0 {
		ctxTimeout, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		recordNew, err := api.CreateDNSRecord(ctxTimeout, cloudflare.ZoneIdentifier(cfg.ZoneID), cloudflare.CreateDNSRecordParams{
			Name:    domain.Hostname,
			Content: address,
			Type:    recordType,
		})
		if err != nil {
			return err
		}
		log.Info().
			Str("id", recordNew.ID).
			Str("hostname", recordNew.Name).
			Str("type", recordNew.Type).
			Str("address", recordNew.Content).
			Msgf("created DNS record")
		return nil
	}

	for _, record := range records {
		ctxTimeout, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()

		if record.Content == address {
			log.Info().
				Str("id", record.ID).
				Str("hostname", record.Name).
				Str("type", record.Type).
				Str("address", record.Content).
				Msgf("skipping DNS record")
			continue
		}

		recordNew, err := api.UpdateDNSRecord(ctxTimeout, cloudflare.ZoneIdentifier(cfg.ZoneID), cloudflare.UpdateDNSRecordParams{
			ID:      record.ID,
			Content: address,
			Proxied: domain.Proxied,
		})
		if err != nil {
			log.Error().Err(err).
				Str("id", record.ID).
				Str("hostname", record.Name).
				Str("type", record.Type).
				Str("address", record.Content).
				Msg("failed to update DNS record")
			return err
		}

		log.Info().
			Str("id", recordNew.ID).
			Str("hostname", recordNew.Name).
			Str("type", recordNew.Type).
			Str("address", recordNew.Content).
			Msgf("updated DNS record")
	}
	return nil
}

func CheckAndUpdateIPv4(ctx context.Context, api *cloudflare.API, cfg *config.Config, domain *config.Domain) error {
	return checkAndUpdate(ctx, api, cfg, domain, RECORD_TYPE_IPV4, currentIPv4)
}

func CheckAndUpdateIPv6(ctx context.Context, api *cloudflare.API, cfg *config.Config, domain *config.Domain) error {
	return checkAndUpdate(ctx, api, cfg, domain, RECORD_TYPE_IPV6, currentIPv6)
}

func zoneIsValid(ctx context.Context, api *cloudflare.API, zoneID string) (bool, error) {
	zones, err := api.ListZones(ctx)

	if err != nil {
		return false, err
	}

	for _, zone := range zones {
		if zone.ID == zoneID {
			return true, nil
		}
	}

	return false, nil
}
