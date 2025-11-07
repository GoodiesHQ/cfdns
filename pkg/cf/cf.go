package cf

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/goodieshq/cfdns/pkg/config"
	"github.com/goodieshq/cfdns/pkg/ipget"
	"github.com/goodieshq/goropo"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	RECORD_TYPE_IPV4 = "A"
	RECORD_TYPE_IPV6 = "AAAA"
)

type CFDNS struct {
	mu         sync.RWMutex    // protects config, api, httpClient, pool
	cfg        config.Config   // current configuration
	api        *cloudflare.API // Cloudflare API client
	httpClient http.Client     // shared HTTP client
	timeout    time.Duration   // HTTP timeout duration
	pool       *goropo.Pool    // worker pool for concurrent tasks
}

// NewCFDNS creates a new Cloudflare DNS updater instance
func NewCFDNS(cfg config.Config) (*CFDNS, error) {
	cfdns := &CFDNS{}
	cfdns.SetConfig(&cfg)
	return cfdns, nil
}

// Close aborts all pending tasks and closes the worker pool
func (cfdns *CFDNS) Close() {
	cfdns.mu.Lock()
	pool := cfdns.pool
	if pool == nil {
		cfdns.mu.Unlock()
		return
	}

	// close while holding write lock so SetConfig/other writers can't race
	pool.Abort()
	cfdns.mu.Unlock()
}

// Wait will wait for all tasks in the pool to complete
func (cfdns *CFDNS) Wait() {
	cfdns.mu.RLock()
	pool := cfdns.pool
	if pool == nil {
		cfdns.mu.RUnlock()
		return
	}

	// keep RLock held while waiting so a concurrent SetConfig (writer) will block
	pool.WaitIdle()
	cfdns.mu.RUnlock()
}

func (cfdns *CFDNS) SetConfig(cfg *config.Config) error {
	if cfg != nil {
		cfdns.mu.Lock()
		defer cfdns.mu.Unlock()

		// create a new cloudflare API client from the scoped token
		api, err := cloudflare.NewWithAPIToken(cfg.Token)
		if err != nil {
			return err
		}

		// swap in the new config and resources
		cfdns.api = api
		cfdns.httpClient = http.Client{
			Timeout: cfg.Timeout,
		}
		if cfdns.pool != nil {
			// close previous pool before replacing
			cfdns.pool.Close()
		}

		cfdns.timeout = cfg.Timeout
		cfdns.pool = goropo.NewPool(cfg.WorkerCount, cfg.WorkerCount*5)

		if cfg.Verbose {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		} else {
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
		}
		cfdns.cfg = *cfg
	}
	return nil
}

// getRecords retrieves DNS records for the given hostname and record type
func (cfdns *CFDNS) getRecords(ctx context.Context, hostname, recordType string) ([]cloudflare.DNSRecord, error) {
	cfdns.mu.RLock()
	defer cfdns.mu.RUnlock()
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

// ZoneIsValid checks if the configured zone ID is valid for the provided API token
func (cfdns *CFDNS) ZoneIsValid(ctx context.Context) (bool, error) {
	cfdns.mu.RLock()
	pool := cfdns.pool
	api := cfdns.api
	zoneID := cfdns.cfg.ZoneID

	fut := goropo.Submit(
		pool,
		ctx,
		func(ctx context.Context) ([]cloudflare.Zone, error) {
			return api.ListZones(ctx)
		},
	)

	zones, err := fut.Await(ctx)
	cfdns.mu.RUnlock()

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

// checkAndUpdate checks the existing DNS records for the given domain and record type,
// and updates or creates the record if the address has changed or does not exist.
// Caller must hold cfdns.mu RLock.
func (cfdns *CFDNS) checkAndUpdate(
	ctx context.Context,
	domain *config.Domain,
	recordType,
	address string,
) error {
	const timeout = time.Second * 10

	// get existing records for this hostname and record type
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
			Msgf("Created new DNS record")
		return nil
	}

	// iterate over existing records and update if the address has changed
	for _, record := range records {
		ctxTimeout, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()

		if record.Content == address && (record.Proxied == nil ||
			domain.Proxied == nil ||
			(*record.Proxied == *domain.Proxied)) {

			log.Debug().
				Str("id", record.ID).
				Str("hostname", record.Name).
				Str("type", record.Type).
				Str("address", record.Content).
				Msgf("Skipping DNS record")
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
				Msg("Failed to update DNS record")
			return err
		}

		log.Info().
			Str("id", recordNew.ID).
			Str("hostname", recordNew.Name).
			Str("type", recordNew.Type).
			Str("address", recordNew.Content).
			Msgf("Updated DNS record")
	}
	return nil
}

func (cfdns *CFDNS) getPublicIPs(ctx context.Context) (string, string) {
	var fut4, fut6 *goropo.Future[string]
	var ipv4, ipv6 string

	if *cfdns.cfg.IPv4 {
		fut4 = goropo.Submit(cfdns.pool, ctx, ipget.GetPublicIPv4)
	}

	if *cfdns.cfg.IPv6 {
		fut6 = goropo.Submit(cfdns.pool, ctx, ipget.GetPublicIPv6)
	}

	if fut4 != nil {
		v, err := fut4.Await(ctx)
		if err != nil {
			log.Error().Err(err).Msg("failed to get public ipv4")
			ipv4 = ""
		} else {
			ipv4 = v
			log.Debug().Str("ipv4", ipv4).Msg("fetched ipv4 address")
		}
	}

	if fut6 != nil {
		v, err := fut6.Await(ctx)
		if err != nil {
			log.Error().Err(err).Msg("failed to get public ipv6")
			ipv6 = ""
		} else {
			ipv6 = v
			log.Debug().Str("ipv6", ipv6).Msg("fetched ipv6 address")
		}
	}

	return ipv4, ipv6
}

func (cfdns *CFDNS) Process(ctx context.Context) {
	cfdns.mu.RLock()
	defer cfdns.mu.RUnlock()

	valid, err := cfdns.ZoneIsValid(ctx)
	if err != nil || !valid {
		log.Error().Err(err).Msg("unable to verify zone and API token, skipping processing cycle")
		return
	}

	// acquire the current public IP addresses for this run
	ipv4, ipv6 := cfdns.getPublicIPs(ctx)

	// make a list of futures for all domain updates, allocate enough for both ipv4 and ipv6
	futs := make([]*goropo.FutureAny, 0, len(cfdns.cfg.Domains)*2)

	// iterate over all configured domains and update their DNS records as needed
	for _, domain := range cfdns.cfg.Domains {
		if *cfdns.cfg.IPv4 && ipv4 != "" {
			fut := goropo.Submit(
				cfdns.pool,
				ctx,
				func(ctx context.Context) (any, error) {
					if err := cfdns.checkAndUpdate(ctx, &domain, RECORD_TYPE_IPV4, ipv4); err != nil {
						log.Error().Err(err).Str("domain", domain.Hostname).Msg("failed to update ipv4 record")
						return nil, err
					}
					return nil, nil
				},
			)
			futs = append(futs, fut)
		}

		if *cfdns.cfg.IPv6 && ipv6 != "" {
			fut := goropo.Submit(
				cfdns.pool,
				ctx,
				func(ctx context.Context) (any, error) {
					if err := cfdns.checkAndUpdate(ctx, &domain, RECORD_TYPE_IPV6, ipv6); err != nil {
						log.Error().Err(err).Str("domain", domain.Hostname).Msg("failed to update ipv6 record")
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
