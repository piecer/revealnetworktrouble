package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultGeoIPBaseURL = "https://ipwho.is/"

type GeoLocation struct {
	City        string  `json:"city,omitempty"`
	Region      string  `json:"region,omitempty"`
	Country     string  `json:"country,omitempty"`
	CountryCode string  `json:"country_code,omitempty"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type ASNInfo struct {
	Number       uint   `json:"number,omitempty"`
	Organization string `json:"organization,omitempty"`
}

type IPMetadata struct {
	Geolocation *GeoLocation `json:"geolocation,omitempty"`
	ASN         *ASNInfo     `json:"asn,omitempty"`
}

type GeoIPLookup interface {
	Lookup(context.Context, net.IP) (IPMetadata, error)
}

type IPWhoIsLookup struct {
	Client  *http.Client
	BaseURL string
	mu      sync.RWMutex
	cache   map[string]IPMetadata
}

func NewIPWhoIsLookup(client *http.Client, baseURL string) *IPWhoIsLookup {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGeoIPBaseURL
	}
	return &IPWhoIsLookup{Client: client, BaseURL: strings.TrimRight(baseURL, "/") + "/", cache: make(map[string]IPMetadata)}
}

func (l *IPWhoIsLookup) Lookup(ctx context.Context, ip net.IP) (IPMetadata, error) {
	address := ip.String()
	l.mu.RLock()
	metadata, ok := l.cache[address]
	l.mu.RUnlock()
	if ok {
		return metadata, nil
	}

	endpoint, err := url.JoinPath(l.BaseURL, address)
	if err != nil {
		return IPMetadata{}, fmt.Errorf("build geoip URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return IPMetadata{}, fmt.Errorf("create geoip request: %w", err)
	}
	resp, err := l.Client.Do(req)
	if err != nil {
		return IPMetadata{}, fmt.Errorf("geoip request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return IPMetadata{}, fmt.Errorf("geoip response status %d", resp.StatusCode)
	}
	var payload struct {
		Success     bool    `json:"success"`
		Message     string  `json:"message"`
		City        string  `json:"city"`
		Region      string  `json:"region"`
		Country     string  `json:"country"`
		CountryCode string  `json:"country_code"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		Connection  struct {
			ASN uint   `json:"asn"`
			Org string `json:"org"`
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return IPMetadata{}, fmt.Errorf("decode geoip response: %w", err)
	}
	if !payload.Success {
		return IPMetadata{}, fmt.Errorf("geoip lookup failed: %s", payload.Message)
	}
	organization := payload.Connection.Org
	if organization == "" {
		organization = payload.Connection.ISP
	}
	metadata = IPMetadata{
		Geolocation: &GeoLocation{City: payload.City, Region: payload.Region, Country: payload.Country, CountryCode: payload.CountryCode, Latitude: payload.Latitude, Longitude: payload.Longitude},
		ASN:         &ASNInfo{Number: payload.Connection.ASN, Organization: organization},
	}
	l.mu.Lock()
	l.cache[address] = metadata
	l.mu.Unlock()
	return metadata, nil
}

func isPublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range nonPublicSpecialPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var nonPublicSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}
