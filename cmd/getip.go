package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ipv4Services = []string{ // URLs to acquire IPv4 address
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://v4.ident.me",
	}
	ipv6Services = []string{ // URLs to acquire IPv6 address
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
		"https://v6.ident.me",
	}
)

const TIMEOUT_DEFAULT = time.Second * 5

func getPublicIP(services []string) (string, error) {
	client := &http.Client{
		Timeout: TIMEOUT_DEFAULT,
	}

	for _, service := range services {
		resp, err := client.Get(service)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		ip, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return strings.TrimSpace(string(ip)), nil
		}
	}

	return "", fmt.Errorf("could not retrieve IP address")
}

func GetPublicIPv4() (string, error) {
	return getPublicIP(ipv4Services)
}

func GetPublicIPv6() (string, error) {
	return getPublicIP(ipv6Services)
}
