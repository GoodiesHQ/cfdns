package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const DEFAULT_FREQUENCY = time.Hour * 1

type Domain struct {
	Hostname string `json:"hostname" yaml:"hostname"`
	Proxied  *bool  `json:"proxied" yaml:"proxied"`
}

type Config struct {
	ZoneID    string        `json:"zone_id" yaml:"zone_id"`     // CloudFlare Zone ID
	Token     string        `json:"token" yaml:"token"`         // CloudFlare zone-scoped token (read/write)
	Frequency time.Duration `json:"frequency" yaml:"frequency"` // Frequency at which to update the domains
	Verbose   bool          `json:"verbose" yaml:"verbose"`     // Verbose logging output
	IPv4      bool          `json:"ipv4" yaml:"ipv4"`           // use IPv4 A records
	IPv6      bool          `json:"ipv6" yaml:"ipv6"`           // use IPv6 AAAA records
	Domains   []Domain      `json:"domains" yaml:"domains"`     // List of domains to update
}

func LoadConfig(filename string) (*Config, error) {
	// Read the file content
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not read file: %w", err)
	}

	// Initialize the Config struct
	var config Config

	// Check the file extension
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json":
		// Parse JSON
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("could not parse JSON config file: %w", err)
		}
	case ".yaml", ".yml":
		// Parse YAML
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("could not parse YAML config file: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported file extension: %s", ext)
	}

	if config.ZoneID == "" {
		return nil, fmt.Errorf("zone id cannot be empty")
	}

	if len(config.Domains) == 0 {
		return nil, fmt.Errorf("domains list cannot be empty")
	}

	if config.Frequency == 0 {
		config.Frequency = DEFAULT_FREQUENCY
	}

	// if neither IPv6 nor IPv6 are explicitly specified, use both
	if !config.IPv4 && !config.IPv6 {
		config.IPv4 = true
		config.IPv6 = true
	}

	return &config, nil
}
