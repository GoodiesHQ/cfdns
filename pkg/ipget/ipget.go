package ipget

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

var (
	// URLs to acquire IPv4 address as a string
	ipv4Services = []string{
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://v4.ident.me",
	}
	// URLs to acquire IPv6 address as a string
	ipv6Services = []string{
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
		"https://v6.ident.me",
	}
)

const TIMEOUT_DEFAULT = time.Second * 5

// drain reads and discards all remaining data from an io.ReadCloser
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
}

// strToIP converts a string to a net.IP, returning nil if invalid
func strToIP(ipStr string) net.IP {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip.To16()
}

// shared HTTP client with timeout
var client = &http.Client{
	Timeout: TIMEOUT_DEFAULT,
}

// shuffle returns a new slice with the elements of the input slice shuffled
func shuffle[T any](items []T) []T {
	shuffled := make([]T, len(items))
	copy(shuffled, items)

	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled
}

// getSmallText is a helper function to get text content from a URL efficiently
func getSmallText(ctx context.Context, url string) (string, error) {
	// create a new request tied to the context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	// perform the request
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		drain(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}

	drain(resp.Body)

	return strings.TrimSpace(string(body)), nil
}

// getPublicIP tries to get the public IP address from a list of services
func getPublicIP(ctx context.Context, services []string) (string, error) {
	services = shuffle(services)
	for _, service := range services {
		logger := log.With().Str("service", service).Logger()
		ipStr, err := getSmallText(ctx, service)
		if err != nil {
			// context-related errors should be returned immediately
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				logger.Error().Err(err).Msg("request context error")
				return "", err
			}

			// all other errors are logged and we continue to the next service
			logger.Warn().Err(err).Msg("failed to acquire IP address from service")
			continue
		}

		// validate the returned IP address
		if ip := strToIP(ipStr); ip != nil {
			return ip.String(), nil
		}
	}

	return "", fmt.Errorf("could not retrieve public IP address")
}

func GetPublicIPv4(ctx context.Context) (string, error) {
	return getPublicIP(ctx, ipv4Services)
}

func GetPublicIPv6(ctx context.Context) (string, error) {
	return getPublicIP(ctx, ipv6Services)
}
