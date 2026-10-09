package diagnostic

import (
	"encoding/json"
	"net"
	"net/netip"
	"sort"
	"time"
	"unicode/utf8"
)

// GeoDetailSnapshot is supplemental text from one validated provider response.
// It is a value snapshot: copying metadata/nodes cannot alias mutable cache data.
// It does not attest to separately aggregated legacy coordinates or ASN.
type GeoDetailSnapshot struct {
	Provider      string
	Source        GeoIPSource
	FetchedAt     time.Time
	ExpiresAt     time.Time
	City          string
	Region        string
	Country       string
	CountryCode   string
	Continent     string
	ContinentCode string
	RegionCode    string
	Postal        string
	Timezone      string
	ISP           string
	NetworkDomain string
}

// decodeGeoDetails is independent of legacy Geo/ASN validation. Its caller has
// already validated the complete JSON syntax, UTF-8 and duplicate keys.
func decodeGeoDetails(data []byte) GeoDetailSnapshot {
	var payload struct {
		Success       bool   `json:"success"`
		City          string `json:"city"`
		Region        string `json:"region"`
		Country       string `json:"country"`
		CountryCode   string `json:"country_code"`
		Continent     string `json:"continent"`
		ContinentCode string `json:"continent_code"`
		RegionCode    string `json:"region_code"`
		Postal        string `json:"postal"`
		Timezone      struct {
			ID string `json:"id"`
		} `json:"timezone"`
		Connection struct {
			ISP    string `json:"isp"`
			Domain string `json:"domain"`
		} `json:"connection"`
	}
	if json.Unmarshal(data, &payload) != nil || !payload.Success {
		return GeoDetailSnapshot{}
	}
	snapshot := GeoDetailSnapshot{
		Provider: "ipwho.is", City: payload.City, Region: payload.Region, Country: payload.Country, CountryCode: payload.CountryCode,
		Continent: payload.Continent, ContinentCode: payload.ContinentCode, RegionCode: payload.RegionCode, Postal: payload.Postal,
		Timezone: payload.Timezone.ID, ISP: payload.Connection.ISP, NetworkDomain: payload.Connection.Domain,
	}
	if !validGeoDetailText(snapshot) {
		return GeoDetailSnapshot{}
	}
	return snapshot
}

func validGeoDetailText(snapshot GeoDetailSnapshot) bool {
	total := 0
	for _, value := range []string{snapshot.City, snapshot.Region, snapshot.Country, snapshot.CountryCode, snapshot.Continent, snapshot.ContinentCode, snapshot.RegionCode, snapshot.Postal, snapshot.Timezone, snapshot.ISP, snapshot.NetworkDomain} {
		if !utf8.ValidString(value) || len(value) > geoIPMaxStringBytes {
			return false
		}
		total += len(value)
	}
	return total > 0 && total <= geoIPMaxBundleBytes
}

const (
	GeoDetailsMaxEntries      = 500
	GeoDetailsMaxTotal        = 6200
	GeoDetailsMaxBytes        = 128 << 10
	GeoDetailsTimestampLayout = "2006-01-02T15:04:05.000Z"
)

// GeoDetailsSidecar is the opt-in transport contract. Empty Entries is always
// an array, never null. Total describes raw observations, not compact survival.
type GeoDetailsSidecar struct {
	SchemaVersion int               `json:"schema_version"`
	Total         int               `json:"total"`
	Omitted       int               `json:"omitted"`
	Entries       []GeoDetailsEntry `json:"entries"`
}

type GeoDetailsEntry struct {
	Address       string      `json:"address"`
	Provider      string      `json:"provider"`
	Source        GeoIPSource `json:"source"`
	City          string      `json:"city,omitempty"`
	Region        string      `json:"region,omitempty"`
	Country       string      `json:"country,omitempty"`
	CountryCode   string      `json:"country_code,omitempty"`
	Continent     string      `json:"continent,omitempty"`
	ContinentCode string      `json:"continent_code,omitempty"`
	RegionCode    string      `json:"region_code,omitempty"`
	Postal        string      `json:"postal,omitempty"`
	Timezone      string      `json:"timezone,omitempty"`
	ISP           string      `json:"isp,omitempty"`
	NetworkDomain string      `json:"network_domain,omitempty"`
	FetchedAt     string      `json:"fetched_at,omitempty"`
	ExpiresAt     string      `json:"expires_at,omitempty"`
}

// BuildGeoDetails visits only typed raw result/attempt/hop observations, once
// in their original order. A later snapshot cannot overwrite or fill holes in
// the first valid one. No coordinates/ASN are merged into this provenance.
func BuildGeoDetails(report Report) *GeoDetailsSidecar {
	byAddress := make(map[string]GeoDetailsEntry)
	for _, result := range report.Results {
		if result.Kind != KindTraceroute {
			continue
		}
		attempts, _ := result.Details["attempts"].([]TraceAttempt)
		for _, attempt := range attempts {
			if !traceAttemptEligible(attempt) {
				continue
			}
			for _, node := range attempt.Topology.Nodes {
				if node.Hop <= 0 || (node.Status != "healthy" && node.Status != "degraded") {
					continue
				}
				ip, err := netip.ParseAddr(node.Address)
				if err != nil || ip.Zone() != "" {
					continue
				}
				ip = ip.Unmap()
				if !isPublicIP(net.IP(ip.AsSlice())) {
					continue
				}
				address := ip.String()
				if _, exists := byAddress[address]; exists {
					continue
				}
				s := node.GeoDetails
				if s.Provider != "ipwho.is" || (s.Source != GeoIPSourceUpstream && s.Source != GeoIPSourceCache) || !validGeoDetailText(s) {
					continue
				}
				entry := GeoDetailsEntry{Address: address, Provider: s.Provider, Source: s.Source,
					City: s.City, Region: s.Region, Country: s.Country, CountryCode: s.CountryCode,
					Continent: s.Continent, ContinentCode: s.ContinentCode, RegionCode: s.RegionCode,
					Postal: s.Postal, Timezone: s.Timezone, ISP: s.ISP, NetworkDomain: s.NetworkDomain}
				if !s.FetchedAt.IsZero() || !s.ExpiresAt.IsZero() {
					if s.FetchedAt.IsZero() || s.ExpiresAt.IsZero() || s.ExpiresAt.Before(s.FetchedAt) || s.FetchedAt.UTC().Year() < 1 || s.ExpiresAt.UTC().Year() > 9999 {
						continue
					}
					entry.FetchedAt = s.FetchedAt.UTC().Format(GeoDetailsTimestampLayout)
					entry.ExpiresAt = s.ExpiresAt.UTC().Format(GeoDetailsTimestampLayout)
				}
				byAddress[address] = entry
				// Impossible for an admitted production report. Fail closed rather
				// than inventing a total for an oversized injected raw dataset.
				if len(byAddress) > GeoDetailsMaxTotal {
					return nil
				}
			}
		}
	}
	entries := make([]GeoDetailsEntry, 0, len(byAddress))
	for _, entry := range byAddress {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Address < entries[j].Address })
	return &GeoDetailsSidecar{SchemaVersion: 1, Total: len(entries), Entries: entries}
}
