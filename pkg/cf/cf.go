package cf

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/goodieshq/cfdns/pkg/config"
	"github.com/goodieshq/cfdns/pkg/ipget"
	"github.com/goodieshq/goropo"
	"github.com/rs/zerolog/log"
)

const (
	RECORD_TYPE_IPV4 = "A"
	RECORD_TYPE_IPV6 = "AAAA"
)

type CFDNS struct {
	mu         sync.Mutex
	cfg        config.Config
	api        *cloudflare.API
	httpClient http.Client
	myIPv4     string
	myIPv6     string
	pool       *goropo.Pool
}

func NewCFDNS(cfg config.Config, options ...cloudflare.Option) (*CFDNS, error) {
	// create a new cloudflare API client from the scoped token
	api, err := cloudflare.NewWithAPIToken(cfg.Token, options...)
	if err != nil {
		return nil, err
	}

	return &CFDNS{
		api: api,
		cfg: cfg,
		httpClient: http.Client{
			Timeout: 5 * time.Second,
		},
		pool: goropo.NewPool(10, 50),
	}, nil
}

func (cfdns *CFDNS) Close() {
	// abort all tasks in the pool and close it
	cfdns.pool.Abort()
	cfdns.pool.Close()
}

func (cfdns *CFDNS) Wait() {
	// wait for all tasks in the pool to complete
	cfdns.pool.WaitIdle()
}

func (cfdns *CFDNS) SetConfig(cfg *config.Config) {
	if cfg != nil {
		cfdns.mu.Lock()
		cfdns.cfg = *cfg
		cfdns.mu.Unlock()
	}
}

func (cfdns *CFDNS) getRecords(ctx context.Context, hostname, recordType string) ([]cloudflare.DNSRecord, error) {
	records, _, err := cfdns.api.ListDNSRecords(
		ctx,
		cloudflare.ZoneIdentifier(cfdns.cfg.ZoneID),
		cloudflare.ListDNSRecordsParams{
			Name: hostname,
			Type: recordType,
		},
	)
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (cfdns *CFDNS) ZoneIsValid(ctx context.Context) (bool, error) {
	zones, err := goropo.Submit(
		cfdns.pool,
		ctx,
		func(ctx context.Context) ([]cloudflare.Zone, error) {
			return cfdns.api.ListZones(ctx)
		},
	).Await(ctx)

	if err != nil {
		return false, err
	}

	for _, zone := range zones {
		if zone.ID == cfdns.cfg.ZoneID {
			return true, nil
		}
	}

	return false, nil
}

func (cfdns *CFDNS) CheckAndUpdateIPv4(ctx context.Context, domain *config.Domain) error {
	if cfdns.myIPv4 == "" {
		return fmt.Errorf("no ipv4 address to update")
	}
	return cfdns.checkAndUpdate(ctx, domain, RECORD_TYPE_IPV4, cfdns.myIPv4)
}

func (cfdns *CFDNS) CheckAndUpdateIPv6(ctx context.Context, domain *config.Domain) error {
	if cfdns.myIPv6 == "" {
		return fmt.Errorf("no ipv6 address to update")
	}
	return cfdns.checkAndUpdate(ctx, domain, RECORD_TYPE_IPV6, cfdns.myIPv6)
}

func (cfdns *CFDNS) checkAndUpdate(
	ctx context.Context,
	domain *config.Domain,
	recordType,
	address string,
) error {
	const timeout = time.Second * 5

	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	records, err := cfdns.getRecords(ctxTimeout, domain.Hostname, recordType)
	if err != nil {
		return err
	}

	// no records found for this hostname, create a new one
	if len(records) == 0 {
		ctxTimeout, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()

		recordNew, err := cfdns.api.CreateDNSRecord(
			ctxTimeout,
			cloudflare.ZoneIdentifier(cfdns.cfg.ZoneID),
			cloudflare.CreateDNSRecordParams{
				Name:    domain.Hostname,
				Content: address,
				Type:    recordType,
				Proxied: domain.Proxied,
			},
		)
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

		if record.Content == address && (record.Proxied == nil ||
			domain.Proxied == nil ||
			(*record.Proxied == *domain.Proxied)) {

			log.Info().
				Str("id", record.ID).
				Str("hostname", record.Name).
				Str("type", record.Type).
				Str("address", record.Content).
				Msgf("skipping DNS record")
			return nil
		}

		recordNew, err := cfdns.api.UpdateDNSRecord(
			ctxTimeout,
			cloudflare.ZoneIdentifier(cfdns.cfg.ZoneID),
			cloudflare.UpdateDNSRecordParams{
				ID:      record.ID,
				Content: address,
				Proxied: domain.Proxied,
			},
		)
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

func (cfdns *CFDNS) setPublicIPs(ctx context.Context) {
	var fut4, fut6 *goropo.Future[string]

	if cfdns.cfg.IPv4 {
		fut4 = goropo.Submit(cfdns.pool, ctx, ipget.GetPublicIPv4)
	} else {
		cfdns.myIPv4 = ""
	}

	if cfdns.cfg.IPv6 {
		fut6 = goropo.Submit(cfdns.pool, ctx, ipget.GetPublicIPv6)
	} else {
		cfdns.myIPv6 = ""
	}

	if fut4 != nil {
		ipv4, err4 := fut4.Await(ctx)
		if err4 != nil {
			cfdns.myIPv4 = ""
			log.Error().Err(err4).Msg("failed to get public ipv4")
		} else if ipv4 != cfdns.myIPv4 {
			cfdns.myIPv4 = ipv4
			log.Info().Str("new_ipv4", ipv4).Msg("updated ipv4 address")
		}
	}

	if fut6 != nil {
		ipv6, err6 := fut6.Await(ctx)
		if err6 != nil {
			cfdns.myIPv6 = ""
			log.Error().Err(err6).Msg("failed to get public ipv6")
		} else if ipv6 != cfdns.myIPv6 {
			cfdns.myIPv6 = ipv6
			log.Info().Str("new_ipv6", ipv6).Msg("updated ipv6 address")
		}
	}
}

func (cfdns *CFDNS) Process(ctx context.Context) {
	// lock the config
	cfdns.mu.Lock()
	defer cfdns.mu.Unlock()

	// acquire and set the current public IP addresses for this instance
	cfdns.setPublicIPs(ctx)
	futs := make([]*goropo.FutureAny, 0, len(cfdns.cfg.Domains)*2)

	// iterate over all configured domains and update their DNS records as needed
	for _, domain := range cfdns.cfg.Domains {
		domain := domain // capture range variable

		if cfdns.cfg.IPv4 {
			fut := goropo.Submit(
				cfdns.pool,
				ctx,
				func(ctx context.Context) (any, error) {
					if err := cfdns.CheckAndUpdateIPv4(ctx, &domain); err != nil {
						log.Error().Err(err).Str("domain", domain.Hostname).Msg("failed to update ipv4 record")
						return nil, err
					}
					return nil, nil
				},
			)
			futs = append(futs, fut)
		}

		if cfdns.cfg.IPv6 {
			fut := goropo.Submit(
				cfdns.pool,
				ctx,
				func(ctx context.Context) (any, error) {
					if err := cfdns.CheckAndUpdateIPv6(ctx, &domain); err != nil {
						log.Error().Err(err).Str("domain", domain.Hostname).Msg("failed to update ipv4 record")
						return nil, err
					}
					return nil, nil
				},
			)
			futs = append(futs, fut)
		}
	}

	for _, fut := range futs {
		_, _ = fut.Await(ctx)
	}
}
