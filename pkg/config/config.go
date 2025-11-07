package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

const DEFAULT_FREQUENCY = time.Hour * 1   // default to updating every hour
const MINIMUM_FREQUENCY = time.Minute * 1 // minimum update frequency is 1 minute
const DEFAULT_TIMEOUT = time.Second * 10  // default timeout for HTTP requests
const MINIMUM_TIMEOUT = time.Second * 1   // minimum timeout for HTTP requests
const DEFAULT_WORKER_COUNT = 10           // default number of concurrent workers
const MINIMUM_WORKER_COUNT = 1            // minimum number of concurrent workers
const MAXIMUM_WORKER_COUNT = 100          // maximum number of concurrent workers

type Domain struct {
	Hostname string `yaml:"hostname"` // FQDN of the domain to update
	Proxied  *bool  `yaml:"proxied"`  // Whether the record is proxied through CloudFlare, nil = leave unchanged
}

type Config struct {
	ZoneID      string        `yaml:"zone_id"`      // CloudFlare Zone ID
	Token       string        `yaml:"token"`        // CloudFlare zone-scoped token (read/write)
	Frequency   time.Duration `yaml:"frequency"`    // Frequency at which to update the domains
	Verbose     bool          `yaml:"verbose"`      // Verbose logging output
	IPv4        *bool         `yaml:"ipv4"`         // use IPv4 A records
	IPv6        *bool         `yaml:"ipv6"`         // use IPv6 AAAA records
	Domains     []Domain      `yaml:"domains"`      // List of domain names to update
	WorkerCount int           `yaml:"worker_count"` // Number of concurrent workers
	Timeout     time.Duration `yaml:"timeout"`      // HTTP timeout duration
}

// Environment variable names for sensitive config values
var reEnv = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Replace ${VAR} with environment variable $VAR. If any referenced VAR is unset, return an error.
func expandEnv(data string) (string, error) {
	missing := map[string]struct{}{}
	data = reEnv.ReplaceAllStringFunc(data, func(m string) string {
		name := reEnv.FindStringSubmatch(m)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			missing[name] = struct{}{}
			return ""
		}
		return val
	})
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		return "", fmt.Errorf("missing environment variables from configuration: %s", strings.Join(names, ", "))
	}

	return data, nil
}

func LoadConfig(filename string) (*Config, error) {
	// Read the file content
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not read file: %w", err)
	}

	data, err := expandEnv(string(raw))
	if err != nil {
		return nil, err
	}

	// Initialize the Config struct
	var config Config

	decoder := yaml.NewDecoder(strings.NewReader(data))
	decoder.SetStrict(true)

	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("could not parse config file: %w", err)
	}

	config.ZoneID = strings.TrimSpace(config.ZoneID)
	if config.ZoneID == "" {
		return nil, fmt.Errorf("zone id cannot be empty")
	}

	config.Token = strings.TrimSpace(config.Token)
	if config.Token == "" {
		return nil, fmt.Errorf("API token cannot be empty")
	}

	if len(config.Domains) == 0 {
		return nil, fmt.Errorf("domains list cannot be empty")
	}

	if config.Frequency == 0 {
		config.Frequency = DEFAULT_FREQUENCY
	}
	if config.Frequency < MINIMUM_FREQUENCY {
		log.Warn().Msgf("frequency %s is too low, setting to minimum of %s", config.Frequency.String(), MINIMUM_FREQUENCY.String())
		config.Frequency = MINIMUM_FREQUENCY
	}

	if config.Timeout == 0 {
		config.Timeout = DEFAULT_TIMEOUT
	}
	if config.Timeout < MINIMUM_TIMEOUT {
		log.Warn().Msgf("timeout %s is too low, setting to minimum of %s", config.Timeout.String(), MINIMUM_TIMEOUT.String())
		config.Timeout = MINIMUM_TIMEOUT
	}

	if config.WorkerCount <= 0 {
		config.WorkerCount = DEFAULT_WORKER_COUNT
	}
	if config.WorkerCount < MINIMUM_WORKER_COUNT {
		log.Warn().Msgf("worker_count %d is too low, setting to minimum of %d", config.WorkerCount, MINIMUM_WORKER_COUNT)
		config.WorkerCount = MINIMUM_WORKER_COUNT
	}
	if config.WorkerCount > MAXIMUM_WORKER_COUNT {
		log.Warn().Msgf("worker_count %d is too high, setting to maximum of %d", config.WorkerCount, MAXIMUM_WORKER_COUNT)
		config.WorkerCount = MAXIMUM_WORKER_COUNT
	}

	t := true
	f := false

	// if neither IPv6 nor IPv6 are explicitly specified, use both
	if config.IPv4 == nil && config.IPv6 == nil {
		config.IPv4 = &t
		config.IPv6 = &t
	}

	if config.IPv4 == nil {
		config.IPv4 = &f
	}

	if config.IPv6 == nil {
		config.IPv6 = &f
	}

	if !*config.IPv4 && !*config.IPv6 {
		return nil, fmt.Errorf("at least one of ipv4 or ipv6 must be enabled")
	}

	return &config, nil
}
