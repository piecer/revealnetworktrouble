package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultGeoIPBaseURL    = "https://ipwho.is/"
	defaultGeoIPMaxEntries = 2048
	defaultGeoIPCacheTTL   = 24 * time.Hour
)

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

type GeoIPCacheConfig struct {
	MaxEntries int
	TTL        time.Duration
	Now        func() time.Time
}

type geoIPCacheEntry struct {
	metadata   IPMetadata
	expiresAt  time.Time
	lastAccess uint64
}

type IPWhoIsLookup struct {
	Client          *http.Client
	BaseURL         string
	Policy          *NetworkPolicy
	clientOnce      sync.Once
	policyClient    *http.Client
	policyClientErr error
	mu              sync.Mutex
	cache           map[string]geoIPCacheEntry
	cacheMaxEntries int
	cacheTTL        time.Duration
	now             func() time.Time
	accessSequence  uint64
}

func NewIPWhoIsLookup(client *http.Client, baseURL string) *IPWhoIsLookup {
	return NewIPWhoIsLookupWithPolicy(client, baseURL, nil)
}

func NewIPWhoIsLookupWithPolicy(client *http.Client, baseURL string, policy *NetworkPolicy) *IPWhoIsLookup {
	return NewIPWhoIsLookupWithConfig(client, baseURL, policy, GeoIPCacheConfig{})
}

func NewIPWhoIsLookupWithConfig(client *http.Client, baseURL string, policy *NetworkPolicy, cacheConfig GeoIPCacheConfig) *IPWhoIsLookup {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGeoIPBaseURL
	}
	if cacheConfig.MaxEntries <= 0 {
		cacheConfig.MaxEntries = defaultGeoIPMaxEntries
	}
	if cacheConfig.TTL <= 0 {
		cacheConfig.TTL = defaultGeoIPCacheTTL
	}
	if cacheConfig.Now == nil {
		cacheConfig.Now = time.Now
	}
	return &IPWhoIsLookup{
		Client: client, BaseURL: strings.TrimRight(baseURL, "/") + "/", Policy: policy,
		cache: make(map[string]geoIPCacheEntry), cacheMaxEntries: cacheConfig.MaxEntries,
		cacheTTL: cacheConfig.TTL, now: cacheConfig.Now,
	}
}

func (l *IPWhoIsLookup) Lookup(ctx context.Context, ip net.IP) (IPMetadata, error) {
	address := ip.String()
	now := l.now()
	l.mu.Lock()
	entry, ok := l.cache[address]
	if ok && now.Before(entry.expiresAt) {
		l.accessSequence++
		entry.lastAccess = l.accessSequence
		l.cache[address] = entry
		l.mu.Unlock()
		return entry.metadata, nil
	}
	if ok {
		delete(l.cache, address)
	}
	l.mu.Unlock()

	endpoint, err := url.JoinPath(l.BaseURL, address)
	if err != nil {
		return IPMetadata{}, fmt.Errorf("build geoip URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return IPMetadata{}, fmt.Errorf("create geoip request: %w", err)
	}
	client := l.Client
	if l.Policy != nil {
		l.clientOnce.Do(func() {
			l.policyClient, l.policyClientErr = geoIPPolicyClient(l.Client, l.Policy, req.URL.Scheme)
		})
		if l.policyClientErr != nil {
			return IPMetadata{}, fmt.Errorf("geoip network policy: %w", l.policyClientErr)
		}
		client = l.policyClient
	}
	resp, err := client.Do(req)
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
	metadata := IPMetadata{
		Geolocation: &GeoLocation{City: payload.City, Region: payload.Region, Country: payload.Country, CountryCode: payload.CountryCode, Latitude: payload.Latitude, Longitude: payload.Longitude},
		ASN:         &ASNInfo{Number: payload.Connection.ASN, Organization: organization},
	}
	now = l.now()
	l.mu.Lock()
	for key, cached := range l.cache {
		if !now.Before(cached.expiresAt) {
			delete(l.cache, key)
		}
	}
	if _, exists := l.cache[address]; !exists && len(l.cache) >= l.cacheMaxEntries {
		var oldestKey string
		var oldestAccess uint64
		for key, cached := range l.cache {
			if oldestKey == "" || cached.lastAccess < oldestAccess {
				oldestKey, oldestAccess = key, cached.lastAccess
			}
		}
		delete(l.cache, oldestKey)
	}
	l.accessSequence++
	l.cache[address] = geoIPCacheEntry{metadata: metadata, expiresAt: now.Add(l.cacheTTL), lastAccess: l.accessSequence}
	l.mu.Unlock()
	return metadata, nil
}

func geoIPPolicyClient(base *http.Client, policy *NetworkPolicy, initialScheme string) (*http.Client, error) {
	client := *base
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if configured, ok := base.Transport.(*http.Transport); ok && configured != nil {
		transport = configured.Clone()
	} else if base.Transport != nil {
		return nil, ErrNetworkPolicyBlocked
	}
	transport.Proxy = nil
	transport.DialContext = policy.DialContext
	transport.DialTLSContext = nil
	client.Transport = transport
	configuredRedirect := base.CheckRedirect
	client.CheckRedirect = func(redirect *http.Request, via []*http.Request) error {
		if strings.EqualFold(initialScheme, "https") && !strings.EqualFold(redirect.URL.Scheme, "https") {
			return errTLSDowngrade
		}
		if _, err := policy.Resolve(redirect.Context(), redirect.URL.Hostname()); err != nil {
			return err
		}
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		if configuredRedirect != nil {
			return configuredRedirect(redirect, via)
		}
		return nil
	}
	return &client, nil
}

func isPublicIP(ip net.IP) bool {
	return IsPublicDiagnosticIP(ip)
}
