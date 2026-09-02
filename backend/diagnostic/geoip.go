package diagnostic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultGeoIPBaseURL    = "https://ipwho.is/"
	defaultGeoIPMaxEntries = 2048
	defaultGeoIPCacheTTL   = 24 * time.Hour

	DefaultGeoIPMaxActive = 8
	DefaultGeoIPMaxQueued = 64
	MaxGeoIPActive        = 64
	MaxGeoIPQueued        = 4096

	defaultGeoIPProviderTimeout = 3 * time.Second
	geoIPMaxResponseBytes       = 1 << 20
	geoIPMaxStringBytes         = 256
	geoIPMaxBundleBytes         = 1536
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

type GeoIPSource string

const (
	GeoIPSourceUpstream GeoIPSource = "upstream"
	GeoIPSourceCache    GeoIPSource = "cache"
)

type IPMetadata struct {
	Geolocation *GeoLocation `json:"geolocation,omitempty"`
	ASN         *ASNInfo     `json:"asn,omitempty"`
	Source      GeoIPSource  `json:"-"`
	FetchedAt   time.Time    `json:"-"`
	ExpiresAt   time.Time    `json:"-"`
}

type GeoIPErrorKind string

const (
	GeoIPErrorNotFound    GeoIPErrorKind = "not_found"
	GeoIPErrorRateLimited GeoIPErrorKind = "rate_limited"
	GeoIPErrorTimeout     GeoIPErrorKind = "timeout"
	GeoIPErrorPolicy      GeoIPErrorKind = "policy"
	GeoIPErrorMalformed   GeoIPErrorKind = "malformed"
	GeoIPErrorUnavailable GeoIPErrorKind = "unavailable"
	GeoIPErrorCancelled   GeoIPErrorKind = "cancelled"
	GeoIPErrorBusy        GeoIPErrorKind = "busy"
)

type GeoIPError struct {
	Kind      GeoIPErrorKind
	Retryable bool
	cause     error
}

func (e *GeoIPError) Error() string {
	if e == nil {
		return "geoip error"
	}
	return "geoip " + string(e.Kind)
}

func (e *GeoIPError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newGeoIPError(kind GeoIPErrorKind, retryable bool, cause error) error {
	return &GeoIPError{Kind: kind, Retryable: retryable, cause: cause}
}

func geoIPContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return newGeoIPError(GeoIPErrorTimeout, true, context.DeadlineExceeded)
	}
	return newGeoIPError(GeoIPErrorCancelled, false, context.Canceled)
}

type GeoIPLookup interface {
	Lookup(context.Context, net.IP) (IPMetadata, error)
}

type GeoIPCacheConfig struct {
	MaxEntries      int
	TTL             time.Duration
	Now             func() time.Time
	MaxActive       int
	MaxQueued       int
	ProviderTimeout time.Duration
}

type geoIPCacheEntry struct {
	metadata   IPMetadata
	expiresAt  time.Time
	lastAccess uint64
}

type geoIPFlight struct {
	generation uint64
	address    string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}

	waiters    int
	terminal   bool
	abandoned  bool
	cancelled  bool
	queueLease bool
	started    bool
	metadata   IPMetadata
	err        error
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
	inflight        map[string]*geoIPFlight
	cacheMaxEntries int
	cacheTTL        time.Duration
	now             func() time.Time
	accessSequence  uint64
	nextGeneration  uint64
	active          chan struct{}
	maxQueued       int
	queued          int
	providerTimeout time.Duration
}

func NewIPWhoIsLookup(client *http.Client, baseURL string) *IPWhoIsLookup {
	return NewIPWhoIsLookupWithPolicy(client, baseURL, nil)
}

func NewIPWhoIsLookupWithPolicy(client *http.Client, baseURL string, policy *NetworkPolicy) *IPWhoIsLookup {
	return NewIPWhoIsLookupWithConfig(client, baseURL, policy, GeoIPCacheConfig{})
}

func NewIPWhoIsLookupWithConfig(client *http.Client, baseURL string, policy *NetworkPolicy, cacheConfig GeoIPCacheConfig) *IPWhoIsLookup {
	if client == nil {
		client = &http.Client{Timeout: defaultGeoIPProviderTimeout}
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
	cacheConfig.MaxActive = boundedGeoIPLimit(cacheConfig.MaxActive, DefaultGeoIPMaxActive, MaxGeoIPActive)
	cacheConfig.MaxQueued = boundedGeoIPLimit(cacheConfig.MaxQueued, DefaultGeoIPMaxQueued, MaxGeoIPQueued)
	if cacheConfig.ProviderTimeout <= 0 {
		cacheConfig.ProviderTimeout = defaultGeoIPProviderTimeout
	}
	return &IPWhoIsLookup{
		Client: client, BaseURL: strings.TrimRight(baseURL, "/") + "/", Policy: policy,
		cache: make(map[string]geoIPCacheEntry), inflight: make(map[string]*geoIPFlight),
		cacheMaxEntries: cacheConfig.MaxEntries, cacheTTL: cacheConfig.TTL, now: cacheConfig.Now,
		active: make(chan struct{}, cacheConfig.MaxActive), maxQueued: cacheConfig.MaxQueued,
		providerTimeout: cacheConfig.ProviderTimeout,
	}
}

func boundedGeoIPLimit(value, fallback, hardMaximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > hardMaximum {
		return hardMaximum
	}
	return value
}

func (l *IPWhoIsLookup) Lookup(ctx context.Context, ip net.IP) (IPMetadata, error) {
	if err := ctx.Err(); err != nil {
		return IPMetadata{}, geoIPContextError(err)
	}
	if ip == nil || (ip.To4() == nil && ip.To16() == nil) {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	address := ip.String()
	now := l.now()

	l.mu.Lock()
	if entry, ok := l.cache[address]; ok {
		if now.Before(entry.expiresAt) {
			l.accessSequence++
			entry.lastAccess = l.accessSequence
			l.cache[address] = entry
			metadata := cloneIPMetadata(entry.metadata)
			metadata.Source = GeoIPSourceCache
			l.mu.Unlock()
			return metadata, nil
		}
		delete(l.cache, address)
	}

	flight := l.inflight[address]
	if flight == nil || flight.abandoned || flight.terminal {
		hasActive := false
		select {
		case l.active <- struct{}{}:
			hasActive = true
		default:
		}
		if !hasActive && l.queued >= l.maxQueued {
			l.mu.Unlock()
			return IPMetadata{}, newGeoIPError(GeoIPErrorBusy, true, nil)
		}
		sharedCtx, cancel := context.WithCancel(context.Background())
		l.nextGeneration++
		flight = &geoIPFlight{
			generation: l.nextGeneration, address: address, ctx: sharedCtx, cancel: cancel,
			done: make(chan struct{}), queueLease: !hasActive,
		}
		if flight.queueLease {
			l.queued++
		}
		l.inflight[address] = flight
		go l.runFlight(flight, hasActive)
	}
	flight.waiters++
	l.mu.Unlock()

	return l.waitForFlight(ctx, flight)
}

func (l *IPWhoIsLookup) waitForFlight(ctx context.Context, flight *geoIPFlight) (IPMetadata, error) {
	for {
		select {
		case <-flight.done:
		case <-ctx.Done():
		}

		l.mu.Lock()
		if flight.terminal {
			flight.waiters--
			metadata, err := cloneIPMetadata(flight.metadata), flight.err
			l.mu.Unlock()
			return metadata, err
		}
		if err := ctx.Err(); err != nil {
			flight.waiters--
			if flight.waiters == 0 {
				flight.abandoned = true
				if l.inflight[flight.address] == flight {
					delete(l.inflight, flight.address)
				}
				// If provider execution has not won the start arbitration, the
				// caller cancellation owns and releases the queue lease now.
				l.releaseQueueLocked(flight)
				l.cancelFlightLocked(flight)
			}
			l.mu.Unlock()
			return IPMetadata{}, geoIPContextError(err)
		}
		l.mu.Unlock()
	}
}

func (l *IPWhoIsLookup) runFlight(flight *geoIPFlight, hasActive bool) {
	if !hasActive {
		select {
		case l.active <- struct{}{}:
			hasActive = true
		case <-flight.ctx.Done():
			l.mu.Lock()
			l.releaseQueueLocked(flight)
			l.mu.Unlock()
			l.completeFlight(flight, IPMetadata{}, newGeoIPError(GeoIPErrorCancelled, false, context.Canceled))
			return
		}
	}

	// This mutex transition is the only winner selection between the last
	// waiter leaving and provider execution starting.
	l.mu.Lock()
	l.releaseQueueLocked(flight)
	cancelled := flight.abandoned || flight.ctx.Err() != nil
	if !cancelled {
		flight.started = true
	}
	l.mu.Unlock()
	if cancelled {
		<-l.active
		l.completeFlight(flight, IPMetadata{}, newGeoIPError(GeoIPErrorCancelled, false, context.Canceled))
		return
	}

	metadata, err := l.callProvider(flight)
	if hasActive {
		<-l.active
	}
	l.completeFlight(flight, metadata, err)
}

func (l *IPWhoIsLookup) callProvider(flight *geoIPFlight) (metadata IPMetadata, err error) {
	defer func() {
		if recover() != nil {
			metadata = IPMetadata{}
			err = newGeoIPError(GeoIPErrorUnavailable, true, nil)
		}
	}()
	providerCtx, cancel := context.WithTimeout(flight.ctx, l.providerTimeout)
	defer cancel()
	return l.fetch(providerCtx, flight.address)
}

func (l *IPWhoIsLookup) completeFlight(flight *geoIPFlight, metadata IPMetadata, err error) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if flight.terminal {
		return
	}
	l.releaseQueueLocked(flight)
	if err == nil {
		metadata.Source = GeoIPSourceUpstream
		metadata.FetchedAt = now
		metadata.ExpiresAt = now.Add(l.cacheTTL)
		metadata = cloneIPMetadata(metadata)
	}
	flight.metadata, flight.err, flight.terminal = metadata, err, true
	current := l.inflight[flight.address] == flight
	if current {
		delete(l.inflight, flight.address)
	}
	if current && err == nil && !flight.abandoned && flight.ctx.Err() == nil && validIPMetadata(metadata) {
		l.storeCacheLocked(flight.address, metadata, now)
	}
	l.cancelFlightLocked(flight)
	close(flight.done)
}

func (l *IPWhoIsLookup) cancelFlightLocked(flight *geoIPFlight) {
	if !flight.cancelled {
		flight.cancelled = true
		flight.cancel()
	}
}

func (l *IPWhoIsLookup) releaseQueueLocked(flight *geoIPFlight) {
	if flight.queueLease {
		flight.queueLease = false
		l.queued--
	}
}

func (l *IPWhoIsLookup) storeCacheLocked(address string, metadata IPMetadata, now time.Time) {
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
	stored := cloneIPMetadata(metadata)
	stored.Source = GeoIPSourceUpstream
	l.cache[address] = geoIPCacheEntry{metadata: stored, expiresAt: metadata.ExpiresAt, lastAccess: l.accessSequence}
}

func (l *IPWhoIsLookup) fetch(ctx context.Context, address string) (IPMetadata, error) {
	endpoint, err := url.JoinPath(l.BaseURL, address)
	if err != nil {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	client := l.Client
	if l.Policy != nil {
		l.clientOnce.Do(func() {
			l.policyClient, l.policyClientErr = geoIPPolicyClient(l.Client, l.Policy, req.URL.Scheme)
		})
		if l.policyClientErr != nil {
			return IPMetadata{}, newGeoIPError(GeoIPErrorPolicy, false, l.policyClientErr)
		}
		client = l.policyClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctx.Err() != nil {
			return IPMetadata{}, geoIPContextError(ctx.Err())
		}
		if errors.Is(err, ErrNetworkPolicyBlocked) || errors.Is(err, errTLSDowngrade) {
			return IPMetadata{}, newGeoIPError(GeoIPErrorPolicy, false, err)
		}
		return IPMetadata{}, newGeoIPError(GeoIPErrorUnavailable, true, nil)
	}
	if resp == nil || resp.Body == nil {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return IPMetadata{}, newGeoIPError(GeoIPErrorNotFound, false, nil)
		case http.StatusTooManyRequests:
			return IPMetadata{}, newGeoIPError(GeoIPErrorRateLimited, true, nil)
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return IPMetadata{}, newGeoIPError(GeoIPErrorTimeout, true, nil)
		default:
			return IPMetadata{}, newGeoIPError(GeoIPErrorUnavailable, resp.StatusCode >= 500, nil)
		}
	}
	return decodeGeoIPResponse(resp.Body)
}

type geoIPPayload struct {
	Success     bool    `json:"success"`
	Message     string  `json:"message"`
	City        string  `json:"city"`
	Region      string  `json:"region"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Connection  struct {
		ASN uint64 `json:"asn"`
		Org string `json:"org"`
		ISP string `json:"isp"`
	} `json:"connection"`
}

func decodeGeoIPResponse(body io.Reader) (IPMetadata, error) {
	data, err := io.ReadAll(io.LimitReader(body, geoIPMaxResponseBytes+1))
	if err != nil {
		return IPMetadata{}, newGeoIPError(GeoIPErrorUnavailable, true, err)
	}
	if len(data) > geoIPMaxResponseBytes || !utf8.Valid(data) {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	if err := validateStrictGeoIPJSON(data); err != nil {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var payload geoIPPayload
	if err := decoder.Decode(&payload); err != nil {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	if !payload.Success {
		return IPMetadata{}, newGeoIPError(GeoIPErrorNotFound, false, nil)
	}
	if payload.Connection.ASN > math.MaxUint32 {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	organization := payload.Connection.Org
	if organization == "" {
		organization = payload.Connection.ISP
	}
	metadata := IPMetadata{
		Geolocation: &GeoLocation{
			City: payload.City, Region: payload.Region, Country: payload.Country,
			CountryCode: payload.CountryCode, Latitude: payload.Latitude, Longitude: payload.Longitude,
		},
		ASN: &ASNInfo{Number: uint(payload.Connection.ASN), Organization: organization},
	}
	if !validProviderStrings(payload, organization) || !validIPMetadata(metadata) {
		return IPMetadata{}, newGeoIPError(GeoIPErrorMalformed, false, nil)
	}
	return metadata, nil
}

func validateStrictGeoIPJSON(data []byte) error {
	if !validJSONSurrogateEscapes(data) {
		return errors.New("invalid JSON string escape")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("GeoIP response must be an object")
	}
	if err := validateStrictJSONObject(decoder, true); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("GeoIP response must contain one object")
	}
	return nil
}

func validateStrictJSONObject(decoder *json.Decoder, root bool) error {
	keys := make(map[string]struct{})
	successSeen := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("object key must be a string")
		}
		if _, duplicate := keys[key]; duplicate {
			return errors.New("duplicate object key")
		}
		keys[key] = struct{}{}
		if root && key != "success" && strings.EqualFold(key, "success") {
			return errors.New("success key must be lowercase")
		}
		if root && key == "success" {
			successSeen = true
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if _, ok := value.(bool); !ok {
				return errors.New("success must be a boolean")
			}
			continue
		}
		if err := validateStrictJSONValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.New("unterminated object")
	}
	if root && !successSeen {
		return errors.New("missing success")
	}
	return nil
}

func validateStrictJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return validateStrictJSONObject(decoder, false)
	case '[':
		for decoder.More() {
			if err := validateStrictJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("unterminated array")
		}
		return nil
	default:
		return errors.New("unexpected closing delimiter")
	}
}

func validJSONSurrogateEscapes(data []byte) bool {
	for index := 0; index < len(data); index++ {
		if data[index] != '"' {
			continue
		}
		index++
		for ; index < len(data); index++ {
			switch data[index] {
			case '"':
				goto stringComplete
			case '\\':
				if index+1 >= len(data) {
					return false
				}
				switch data[index+1] {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
					index++
				case 'u':
					value, ok := jsonHexQuad(data, index+2)
					if !ok {
						return false
					}
					if value >= 0xD800 && value <= 0xDBFF {
						pair := index + 6
						if pair+5 >= len(data) || data[pair] != '\\' || data[pair+1] != 'u' {
							return false
						}
						low, ok := jsonHexQuad(data, pair+2)
						if !ok || low < 0xDC00 || low > 0xDFFF {
							return false
						}
						index = pair + 5
					} else if value >= 0xDC00 && value <= 0xDFFF {
						return false
					} else {
						index += 5
					}
				default:
					return false
				}
			default:
				if data[index] < 0x20 {
					return false
				}
			}
		}
		return false
	stringComplete:
	}
	return true
}

func jsonHexQuad(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, digit := range data[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func validProviderStrings(payload geoIPPayload, organization string) bool {
	stringsToCheck := []string{payload.Message, payload.City, payload.Region, payload.Country, payload.CountryCode, payload.Connection.Org, payload.Connection.ISP, organization}
	total := 0
	for _, value := range stringsToCheck {
		if !utf8.ValidString(value) || len(value) > geoIPMaxStringBytes {
			return false
		}
		total += len(value)
	}
	return total <= geoIPMaxBundleBytes
}

func validIPMetadata(metadata IPMetadata) bool {
	if metadata.Geolocation == nil || metadata.ASN == nil {
		return false
	}
	geo := metadata.Geolocation
	if math.IsNaN(geo.Latitude) || math.IsInf(geo.Latitude, 0) || geo.Latitude < -90 || geo.Latitude > 90 {
		return false
	}
	if math.IsNaN(geo.Longitude) || math.IsInf(geo.Longitude, 0) || geo.Longitude < -180 || geo.Longitude > 180 {
		return false
	}
	if code := geo.CountryCode; code != "" && (len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z') {
		return false
	}
	values := []string{geo.City, geo.Region, geo.Country, geo.CountryCode, metadata.ASN.Organization}
	total := 0
	for _, value := range values {
		if !utf8.ValidString(value) || len(value) > geoIPMaxStringBytes {
			return false
		}
		total += len(value)
	}
	return total <= geoIPMaxBundleBytes
}

func cloneIPMetadata(metadata IPMetadata) IPMetadata {
	cloned := metadata
	if metadata.Geolocation != nil {
		geo := *metadata.Geolocation
		cloned.Geolocation = &geo
	}
	if metadata.ASN != nil {
		asn := *metadata.ASN
		cloned.ASN = &asn
	}
	return cloned
}

func geoIPPolicyClient(base *http.Client, policy *NetworkPolicy, initialScheme string) (*http.Client, error) {
	client := *base
	transport, err := newPolicyTransport(base.Transport, policy)
	if err != nil {
		return nil, err
	}
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
