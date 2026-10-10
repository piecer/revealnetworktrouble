package diagnostic

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

const IPContextMaxResponseBytes = 16384

var ErrIPContextBusy = errors.New("IP context capacity unavailable")
var ErrIPContextInvalid = errors.New("invalid IP context")

type IPContext struct {
	SchemaVersion int                   `json:"schema_version"`
	Address       string                `json:"address"`
	Source        string                `json:"source"`
	FetchedAt     string                `json:"fetched_at"`
	ExpiresAt     string                `json:"expires_at"`
	ReverseDNS    IPContextReverse      `json:"reverse_dns"`
	Registration  IPContextRegistration `json:"registration"`
	Routing       IPContextRouting      `json:"routing"`
}
type IPContextName struct {
	Name          string `json:"name"`
	ForwardStatus string `json:"forward_status"`
}
type IPContextReverse struct {
	Status    string          `json:"status"`
	Source    string          `json:"source"`
	FetchedAt string          `json:"fetched_at"`
	Names     []IPContextName `json:"names"`
	Omitted   int             `json:"omitted"`
}
type IPContextRegistration struct {
	Status       string `json:"status"`
	Source       string `json:"source"`
	FetchedAt    string `json:"fetched_at"`
	Registry     string `json:"registry,omitempty"`
	StartAddress string `json:"start_address,omitempty"`
	EndAddress   string `json:"end_address,omitempty"`
	Handle       string `json:"handle,omitempty"`
	Name         string `json:"name,omitempty"`
	Type         string `json:"type,omitempty"`
	Country      string `json:"country,omitempty"`
	Organization string `json:"organization,omitempty"`
	RegisteredAt string `json:"registered_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}
type IPContextRouting struct {
	Status    string            `json:"status"`
	Source    string            `json:"source"`
	FetchedAt string            `json:"fetched_at"`
	Prefix    string            `json:"prefix,omitempty"`
	Origins   []IPContextOrigin `json:"origins"`
	Omitted   int               `json:"omitted"`
}
type IPContextOrigin struct {
	ASN  uint32        `json:"asn"`
	RPKI IPContextRPKI `json:"rpki"`
}
type IPContextRPKI struct {
	Status    string `json:"status"`
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	Validity  string `json:"validity,omitempty"`
}

type IPContextResolver interface {
	IPResolver
	LookupAddr(context.Context, string) ([]string, error)
}
type IPContextHTTP func(context.Context, string) (int, []byte, error)
type IPContextOptions struct {
	Now            func() time.Time
	PolicyResolver IPResolver
	Dialer         Dialer
	TLSRoots       *x509.CertPool
	Resolver       IPContextResolver
	HTTP           IPContextHTTP
}
type IPContextService struct {
	now       func() time.Time
	resolver  IPContextResolver
	http      IPContextHTTP
	transport *ipContextHTTPAdapter
	mu        sync.Mutex
	flights   map[string]*ipContextFlight
	cache     map[string]ipContextCacheEntry
	sequence  uint64
	active    int
	draining  bool
	changed   chan struct{}
}

func NewIPContextService(options IPContextOptions) *IPContextService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Resolver == nil {
		options.Resolver = net.DefaultResolver
	}
	var transport *ipContextHTTPAdapter
	if options.HTTP == nil {
		transport = newIPContextHTTPAdapter(options.PolicyResolver, options.Dialer, options.TLSRoots)
		options.HTTP = transport.Get
	}
	return &IPContextService{now: func() time.Time { return contextAcquisitionClock(options.Now) }, transport: transport, resolver: options.Resolver, http: options.HTTP, cache: map[string]ipContextCacheEntry{}, flights: map[string]*ipContextFlight{}, changed: make(chan struct{})}
}

// Clock failure is invalid local service state, never invented current time.
func contextAcquisitionClock(clock func() time.Time) (now time.Time) {
	defer func() {
		if recover() != nil {
			now = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
		}
	}()
	return clock()
}
func ipContextTime(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}
func MarshalIPContext(value IPContext) ([]byte, error) {
	// Check arbitrary text before encoding/json can replace invalid UTF-8.
	for _, text := range []string{value.Registration.Handle, value.Registration.Name, value.Registration.Type, value.Registration.Organization} {
		if _, valid := contextText(text); !valid {
			return nil, ErrIPContextInvalid
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, ErrIPContextInvalid
	}
	data = append(data, '\n')
	if len(data) > IPContextMaxResponseBytes {
		return nil, ErrIPContextInvalid
	}
	if _, err := ParseIPContext(data, value.Address); err != nil {
		return nil, err
	}
	return data, nil
}
