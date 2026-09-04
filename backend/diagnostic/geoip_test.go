package diagnostic

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func validGeoIPBody(city string) string {
	return `{"success":true,"city":"` + city + `","country_code":"US","latitude":37.4,"longitude":-122.1,"connection":{"asn":15169,"org":"Example"}}`
}

func geoIPResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func requireGeoIPError(t *testing.T, err error, kind GeoIPErrorKind, retryable bool) {
	t.Helper()
	var typed *GeoIPError
	if !errors.As(err, &typed) || typed.Kind != kind || typed.Retryable != retryable {
		t.Fatalf("error = %#v, want kind=%q retryable=%v", err, kind, retryable)
	}
}

func TestIPWhoIsLookupCoalescesSameKeyAndAllowsPartialWaiterCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		if calls == 1 {
			close(started)
		}
		mu.Unlock()
		<-release
		return geoIPResponse(validGeoIPBody("shared")), nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{MaxActive: 1, MaxQueued: 2})

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { _, err := lookup.Lookup(firstCtx, net.ParseIP("8.8.8.8")); firstResult <- err }()
	<-started
	go func() {
		metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
		if err == nil && metadata.Geolocation.City != "shared" {
			err = errors.New("wrong shared result")
		}
		secondResult <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		lookup.mu.Lock()
		flight := lookup.inflight["8.8.8.8"]
		waiters := 0
		if flight != nil {
			waiters = flight.waiters
		}
		lookup.mu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second waiter did not join")
		}
		time.Sleep(time.Millisecond)
	}
	cancelFirst()
	requireGeoIPError(t, <-firstResult, GeoIPErrorCancelled, false)
	close(release)
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestIPWhoIsLookupAbandonedOldGenerationCannotOverwriteSuccessor(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			close(firstStarted)
			<-releaseFirst // deliberately ignore request cancellation
			return geoIPResponse(validGeoIPBody("old")), nil
		}
		return geoIPResponse(validGeoIPBody("new")), nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{MaxActive: 2, MaxQueued: 2})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := lookup.Lookup(ctx, net.ParseIP("8.8.8.8")); first <- err }()
	<-firstStarted
	cancel()
	requireGeoIPError(t, <-first, GeoIPErrorCancelled, false)

	metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil || metadata.Geolocation.City != "new" {
		t.Fatalf("successor metadata=%+v err=%v", metadata, err)
	}
	close(releaseFirst)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		count := calls
		mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old provider did not return")
		}
		time.Sleep(time.Millisecond)
	}
	metadata, err = lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil || metadata.Geolocation.City != "new" || metadata.Source != GeoIPSourceCache {
		t.Fatalf("late old generation changed cache: metadata=%+v err=%v", metadata, err)
	}
}

func TestIPWhoIsLookupQueueLimitsCancellationAndActiveLifetime(t *testing.T) {
	started := make(chan string, 3)
	releases := map[string]chan struct{}{"8.8.8.8": make(chan struct{}), "1.1.1.1": make(chan struct{})}
	var mu sync.Mutex
	active, peak := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		address := strings.TrimPrefix(r.URL.Path, "/")
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		started <- address
		<-releases[address]
		mu.Lock()
		active--
		mu.Unlock()
		return geoIPResponse(validGeoIPBody(address)), nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{MaxActive: 1, MaxQueued: 1})
	first := make(chan error, 1)
	go func() { _, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8")); first <- err }()
	<-started
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() { _, err := lookup.Lookup(queuedCtx, net.ParseIP("1.1.1.1")); queued <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		lookup.mu.Lock()
		queuedCount := lookup.queued
		lookup.mu.Unlock()
		if queuedCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not enter queue")
		}
		time.Sleep(time.Millisecond)
	}
	_, err := lookup.Lookup(context.Background(), net.ParseIP("9.9.9.9"))
	requireGeoIPError(t, err, GeoIPErrorBusy, true)
	cancelQueued()
	requireGeoIPError(t, <-queued, GeoIPErrorCancelled, false)
	close(releases["8.8.8.8"])
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 1 || active != 0 {
		t.Fatalf("peak=%d active=%d", peak, active)
	}
}

func TestIPWhoIsLookupCancelledIgnoredProviderRetainsActiveLease(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "8.8.8.8":
			close(firstStarted)
			<-releaseFirst // intentionally ignores the request context
			return geoIPResponse(validGeoIPBody("late")), nil
		default:
			close(secondStarted)
			return geoIPResponse(validGeoIPBody("next")), nil
		}
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{MaxActive: 1, MaxQueued: 1})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := lookup.Lookup(ctx, net.ParseIP("8.8.8.8")); first <- err }()
	<-firstStarted
	cancel()
	requireGeoIPError(t, <-first, GeoIPErrorCancelled, false)

	second := make(chan error, 1)
	go func() { _, err := lookup.Lookup(context.Background(), net.ParseIP("1.1.1.1")); second <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		lookup.mu.Lock()
		queued := lookup.queued
		lookup.mu.Unlock()
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successor was not queued behind ignored provider")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-secondStarted:
		t.Fatal("active lease released on waiter cancellation")
	default:
	}
	_, err := lookup.Lookup(context.Background(), net.ParseIP("9.9.9.9"))
	requireGeoIPError(t, err, GeoIPErrorBusy, true)
	close(releaseFirst)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("queued successor did not start after provider returned")
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestIPWhoIsLookupCompletionWinsBeforeCallerCancellation(t *testing.T) {
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		<-release
		return geoIPResponse(validGeoIPBody("complete")), nil
	})}
	lookup := NewIPWhoIsLookup(client, "https://geo.example/")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		metadata IPMetadata
		err      error
	}, 1)
	go func() {
		metadata, err := lookup.Lookup(ctx, net.ParseIP("8.8.8.8"))
		result <- struct {
			metadata IPMetadata
			err      error
		}{metadata, err}
	}()
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		lookup.mu.Lock()
		_, cached := lookup.cache["8.8.8.8"]
		lookup.mu.Unlock()
		if cached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not publish")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	got := <-result
	if got.err != nil || got.metadata.Geolocation.City != "complete" {
		t.Fatalf("terminal completion lost arbitration: metadata=%+v err=%v", got.metadata, got.err)
	}
}

func TestIPWhoIsLookupRecoversProviderPanicAndReleasesLease(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			panic("secret provider panic")
		}
		return geoIPResponse(validGeoIPBody("recovered")), nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{MaxActive: 1, MaxQueued: 1})
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	requireGeoIPError(t, err, GeoIPErrorUnavailable, true)
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("panic prose reflected: %v", err)
	}
	metadata, err := lookup.Lookup(context.Background(), net.ParseIP("1.1.1.1"))
	if err != nil || metadata.Geolocation.City != "recovered" {
		t.Fatalf("lease was not released: metadata=%+v err=%v", metadata, err)
	}
}

func TestIPWhoIsLookupStrictResponseMatrix(t *testing.T) {
	large := bytes.Repeat([]byte(" "), geoIPMaxResponseBytes+1)
	cases := []struct {
		name string
		body []byte
	}{
		{"max-plus-one", large},
		{"second-object", []byte(validGeoIPBody("ok") + `{}`)},
		{"trailing-byte", []byte(validGeoIPBody("ok") + `x`)},
		{"array", []byte(`[]`)},
		{"null", []byte(`null`)},
		{"invalid-utf8", []byte{'{', '"', 's', 'u', 'c', 'c', 'e', 's', 's', '"', ':', 't', 'r', 'u', 'e', ',', '"', 'c', 'i', 't', 'y', '"', ':', '"', 0xff, '"', '}'}},
		{"overlong-string", []byte(validGeoIPBody(strings.Repeat("x", geoIPMaxStringBytes+1)))},
		{"invalid-country", []byte(`{"success":true,"country_code":"usa","latitude":1,"longitude":1}`)},
		{"invalid-latitude", []byte(`{"success":true,"country_code":"US","latitude":91,"longitude":1}`)},
		{"invalid-longitude", []byte(`{"success":true,"country_code":"US","latitude":1,"longitude":181}`)},
		{"invalid-asn", []byte(`{"success":true,"country_code":"US","latitude":1,"longitude":1,"connection":{"asn":-1}}`)},
		{"missing-success", []byte(`{"city":"missing"}`)},
		{"null-success", []byte(`{"success":null}`)},
		{"uppercase-success", []byte(`{"Success":true}`)},
		{"exact-and-uppercase-success", []byte(`{"success":true,"Success":false}`)},
		{"escaped-uppercase-success", []byte(`{"\u0053uccess":true}`)},
		{"string-success", []byte(`{"success":"true"}`)},
		{"duplicate-success", []byte(`{"success":true,"success":false}`)},
		{"duplicate-city", []byte(`{"success":true,"city":"first","city":"second"}`)},
		{"escaped-duplicate-city", []byte(`{"success":true,"city":"first","c\u0069ty":"second"}`)},
		{"duplicate-connection-asn", []byte(`{"success":true,"connection":{"asn":1,"asn":2}}`)},
		{"duplicate-unknown-nested", []byte(`{"success":true,"unknown":{"nested":{"key":1,"key":2}}}`)},
		{"unpaired-high-surrogate", []byte(`{"success":true,"city":"\uD800"}`)},
		{"unpaired-low-surrogate", []byte(`{"success":true,"city":"\uDC00"}`)},
		{"wrong-order-surrogates", []byte(`{"success":true,"city":"\uDC00\uD800"}`)},
		{"high-followed-by-non-low", []byte(`{"success":true,"city":"\uD800\u0041"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(tc.body))}, nil
			})}
			lookup := NewIPWhoIsLookup(client, "https://geo.example/")
			for range 2 {
				_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
				requireGeoIPError(t, err, GeoIPErrorMalformed, false)
			}
			if calls != 2 || len(lookup.cache) != 0 {
				t.Fatalf("malformed response published: calls=%d cache=%d", calls, len(lookup.cache))
			}
		})
	}

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return geoIPResponse(validGeoIPBody("ok") + " \n	"), nil
	})}
	if _, err := NewIPWhoIsLookup(client, "https://geo.example/").Lookup(context.Background(), net.ParseIP("8.8.8.8")); err != nil {
		t.Fatalf("trailing whitespace rejected: %v", err)
	}
	exact := validGeoIPBody("boundary")
	exact += strings.Repeat(" ", geoIPMaxResponseBytes-len(exact))
	client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return geoIPResponse(exact), nil
	})}
	if _, err := NewIPWhoIsLookup(client, "https://geo.example/").Lookup(context.Background(), net.ParseIP("8.8.8.8")); err != nil {
		t.Fatalf("exact maximum response rejected: %v", err)
	}

	accepted := []struct {
		name string
		body string
		city string
	}{
		{"surrogate-pair", `{"success":true,"city":"\uD83D\uDE00"}`, "😀"},
		{"string-delimiters-and-escapes", `{"success":true,"city":"[]{} quote=\" slash=\\","unknown":{"value":"[\"}\"]"}}`, `[]{} quote=" slash=\`},
		{"unknown-large-number", `{"success":true,"city":"number","unknown":{"value":1e400}}`, "number"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			metadata, err := decodeGeoIPResponse(strings.NewReader(tc.body))
			if err != nil || metadata.Geolocation.City != tc.city {
				t.Fatalf("metadata=%+v err=%v", metadata, err)
			}
		})
	}
}

type geoIPReadErrorBody struct{}

func (geoIPReadErrorBody) Read([]byte) (int, error) { return 0, errors.New("provider body failed") }
func (geoIPReadErrorBody) Close() error             { return nil }

func TestIPWhoIsLookupReadFailureCoalescesAndIsNotPublished(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		once.Do(func() { close(started) })
		<-release
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: geoIPReadErrorBody{}}, nil
	})}
	lookup := NewIPWhoIsLookup(client, "https://geo.example/")
	results := make(chan error, 2)
	go func() { _, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8")); results <- err }()
	<-started
	go func() { _, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8")); results <- err }()
	waitForGeoIPWaiters(t, lookup, "8.8.8.8", 2)
	close(release)
	for range 2 {
		requireGeoIPError(t, <-results, GeoIPErrorUnavailable, true)
	}
	if calls != 1 || len(lookup.cache) != 0 {
		t.Fatalf("read failure published: calls=%d cache=%d", calls, len(lookup.cache))
	}
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	requireGeoIPError(t, err, GeoIPErrorUnavailable, true)
	if calls != 2 || len(lookup.cache) != 0 {
		t.Fatalf("read failure cached: calls=%d cache=%d", calls, len(lookup.cache))
	}
}

func waitForGeoIPWaiters(t *testing.T, lookup *IPWhoIsLookup, address string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		lookup.mu.Lock()
		flight := lookup.inflight[address]
		got := 0
		if flight != nil {
			got = flight.waiters
		}
		lookup.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters=%d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestIPWhoIsLookupTypedStatusErrorsAreSafeAndNotCached(t *testing.T) {
	cases := []struct {
		status    int
		kind      GeoIPErrorKind
		retryable bool
	}{
		{http.StatusNotFound, GeoIPErrorNotFound, false},
		{http.StatusTooManyRequests, GeoIPErrorRateLimited, true},
		{http.StatusGatewayTimeout, GeoIPErrorTimeout, true},
		{http.StatusServiceUnavailable, GeoIPErrorUnavailable, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("provider secret"))}, nil
			})}
			lookup := NewIPWhoIsLookup(client, "https://geo.example/")
			for range 2 {
				_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
				requireGeoIPError(t, err, tc.kind, tc.retryable)
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("provider prose reflected: %v", err)
				}
			}
			if calls != 2 {
				t.Fatalf("error cached: calls=%d", calls)
			}
		})
	}
}

func TestIPWhoIsLookupProviderTimeoutIsIndependentAndTyped(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{ProviderTimeout: 5 * time.Millisecond})
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	requireGeoIPError(t, err, GeoIPErrorTimeout, true)
}

func TestIPWhoIsLookupConfigAppliesDefaultsAndHardMaxima(t *testing.T) {
	defaults := NewIPWhoIsLookup(nil, "https://geo.example/")
	if cap(defaults.active) != DefaultGeoIPMaxActive || defaults.maxQueued != DefaultGeoIPMaxQueued {
		t.Fatalf("defaults active=%d queued=%d", cap(defaults.active), defaults.maxQueued)
	}
	bounded := NewIPWhoIsLookupWithConfig(nil, "https://geo.example/", nil, GeoIPCacheConfig{MaxActive: MaxGeoIPActive + 1, MaxQueued: MaxGeoIPQueued + 1})
	if cap(bounded.active) != MaxGeoIPActive || bounded.maxQueued != MaxGeoIPQueued {
		t.Fatalf("hard maxima active=%d queued=%d", cap(bounded.active), bounded.maxQueued)
	}
}

func TestGeoIPMetadataValidationRejectsNonFiniteAndOversizedBundle(t *testing.T) {
	metadata := IPMetadata{Geolocation: &GeoLocation{CountryCode: "US", Latitude: math.NaN()}, ASN: &ASNInfo{}}
	if validIPMetadata(metadata) {
		t.Fatal("NaN latitude accepted")
	}
	metadata.Geolocation.Latitude = 1
	metadata.Geolocation.Longitude = math.Inf(1)
	if validIPMetadata(metadata) {
		t.Fatal("infinite longitude accepted")
	}
	bundle := `{"success":true,"city":"` + strings.Repeat("a", geoIPMaxStringBytes) + `","region":"` + strings.Repeat("b", geoIPMaxStringBytes) + `","country":"` + strings.Repeat("c", geoIPMaxStringBytes) + `","country_code":"US","latitude":1,"longitude":1,"connection":{"org":"` + strings.Repeat("d", geoIPMaxStringBytes) + `","isp":"` + strings.Repeat("e", geoIPMaxStringBytes) + `"}}`
	_, err := decodeGeoIPResponse(strings.NewReader(bundle))
	requireGeoIPError(t, err, GeoIPErrorMalformed, false)
}

func TestIPWhoIsLookupClonesCacheMetadataAndTracksFreshness(t *testing.T) {
	now := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return geoIPResponse(validGeoIPBody("original")), nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example/", nil, GeoIPCacheConfig{TTL: time.Hour, Now: func() time.Time { return now }})
	first, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil || first.Source != GeoIPSourceUpstream || !first.FetchedAt.Equal(now) || !first.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("upstream metadata=%+v err=%v", first, err)
	}
	first.Geolocation.City = "mutated"
	first.ASN.Organization = "mutated"
	second, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil || second.Source != GeoIPSourceCache || second.Geolocation.City != "original" || second.ASN.Organization != "Example" {
		t.Fatalf("cache alias leaked: metadata=%+v err=%v", second, err)
	}
	second.Geolocation.City = "mutated-again"
	third, _ := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if third.Geolocation.City != "original" {
		t.Fatalf("return alias leaked: %+v", third)
	}
}

func TestIPWhoIsLookupPublicPolicyBlocksPrivateProviderSink(t *testing.T) {
	lookup := NewIPWhoIsLookupWithPolicy(nil, "http://127.0.0.1/", NewNetworkPolicy(nil, nil))
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if !errors.Is(err, ErrNetworkPolicyBlocked) {
		t.Fatalf("private GeoIP provider was not blocked: %v", err)
	}
}

func TestIPWhoIsLookupPublicPolicyDisablesLegacyDialTLSBypass(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	client := legacyDialTLSClient(server.Listener.Addr().String())
	lookup := NewIPWhoIsLookupWithPolicy(client, "https://127.0.0.1/", NewNetworkPolicy(nil, nil))
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if !errors.Is(err, ErrNetworkPolicyBlocked) {
		t.Fatalf("legacy DialTLS bypassed GeoIP policy: %v", err)
	}
}

func TestIPWhoIsLookupTLSDowngradeIsPolicyFailureAndIsNotPublished(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "http://downgrade.test/8.8.8.8", http.StatusFound)
	}))
	defer server.Close()
	resolver := &resolverSequence{addresses: [][]net.IPAddr{ipAnswers("93.184.216.34"), ipAnswers("93.184.216.34")}}
	dialer := &mappingDialer{target: server.Listener.Addr().String()}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	lookup := NewIPWhoIsLookupWithPolicy(client, "https://geo.test/", NewNetworkPolicy(resolver, dialer))
	for range 2 {
		_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
		requireGeoIPError(t, err, GeoIPErrorPolicy, false)
		if !errors.Is(err, errTLSDowngrade) {
			t.Fatalf("downgrade cause lost: %v", err)
		}
	}
	if requests != 2 || len(lookup.cache) != 0 {
		t.Fatalf("downgrade published: requests=%d cache=%d", requests, len(lookup.cache))
	}
}

func TestIPWhoIsLookupRejectsNoncanonicalIPBeforeProviderCall(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return geoIPResponse(validGeoIPBody("unexpected")), nil
	})}
	lookup := NewIPWhoIsLookup(client, "https://geo.example/")
	malformed := net.IP{1, 2, 3}
	if malformed == nil || malformed.To4() != nil || malformed.To16() != nil {
		t.Fatal("test IP is not malformed as intended")
	}
	_, err := lookup.Lookup(context.Background(), malformed)
	requireGeoIPError(t, err, GeoIPErrorMalformed, false)
	if calls != 0 || len(lookup.cache) != 0 || len(lookup.inflight) != 0 {
		t.Fatalf("malformed IP admitted: calls=%d cache=%d inflight=%d", calls, len(lookup.cache), len(lookup.inflight))
	}
}

func TestIPWhoIsLookupPublicPolicyRechecksRedirectAndBypassesProxy(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Host == "first.test" {
			http.Redirect(w, r, "http://second.test/8.8.8.8", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	resolver := &resolverSequence{addresses: [][]net.IPAddr{
		ipAnswers("93.184.216.34"),
		ipAnswers("93.184.216.35"),
		ipAnswers("127.0.0.1"),
	}}
	dialer := &mappingDialer{target: server.Listener.Addr().String()}
	proxyCalled := false
	client := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		proxyCalled = true
		return url.Parse("http://127.0.0.1:9")
	}}}
	lookup := NewIPWhoIsLookupWithPolicy(client, "http://first.test/", NewNetworkPolicy(resolver, dialer))
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if !errors.Is(err, ErrNetworkPolicyBlocked) || proxyCalled || requests != 1 || len(dialer.calls) != 1 || resolver.calls != 3 {
		t.Fatalf("err=%v proxy=%v requests=%d dials=%+v resolver_calls=%d", err, proxyCalled, requests, dialer.calls, resolver.calls)
	}
}

func TestIPWhoIsLookupMapsAndCachesResponse(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/8.8.8.8" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"city":"Mountain View","region":"California","country":"United States","country_code":"US","latitude":37.4,"longitude":-122.1,"connection":{"asn":15169,"org":"Google LLC"}}`))}, nil
	})}

	lookup := NewIPWhoIsLookup(client, "https://geo.example.test/")
	for range 2 {
		metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Geolocation.City != "Mountain View" || metadata.ASN.Number != 15169 || metadata.ASN.Organization != "Google LLC" {
			t.Fatalf("metadata = %+v", metadata)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestIPWhoIsLookupCacheIsBoundedLRUAndExpires(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example.test/", nil, GeoIPCacheConfig{
		MaxEntries: 2,
		TTL:        time.Hour,
		Now:        func() time.Time { return now },
	})
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if _, err := lookup.Lookup(context.Background(), net.ParseIP(address)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 || len(lookup.cache) != 2 {
		t.Fatalf("calls=%d cache=%d", calls, len(lookup.cache))
	}
	if _, err := lookup.Lookup(context.Background(), net.ParseIP("1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(lookup.cache) != 2 {
		t.Fatalf("LRU eviction failed: calls=%d cache=%d", calls, len(lookup.cache))
	}
	now = now.Add(2 * time.Hour)
	if _, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8")); err != nil {
		t.Fatal(err)
	}
	if calls != 5 || len(lookup.cache) > 2 {
		t.Fatalf("TTL refresh failed: calls=%d cache=%d", calls, len(lookup.cache))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type recordingGeoIP struct {
	mu    sync.Mutex
	calls map[string]int
}

type failingGeoIP struct{}

func (failingGeoIP) Lookup(context.Context, net.IP) (IPMetadata, error) {
	return IPMetadata{}, errors.New("provider unavailable")
}

func (r *recordingGeoIP) Lookup(_ context.Context, ip net.IP) (IPMetadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[ip.String()]++
	return IPMetadata{
		Geolocation: &GeoLocation{City: "Seoul", Country: "South Korea", CountryCode: "KR", Latitude: 37.56, Longitude: 126.97},
		ASN:         &ASNInfo{Number: 15169, Organization: "Example Network"},
	}, nil
}

func TestEnrichTopologiesOnlyLooksUpUniquePublicIPs(t *testing.T) {
	lookup := &recordingGeoIP{calls: make(map[string]int)}
	attempts := []TraceAttempt{
		{Status: StatusUnreachable, Topology: &Topology{Nodes: []TopologyNode{{Address: "10.0.0.1"}, {Address: "8.8.8.8"}, {Address: "8.8.8.8"}}}},
		{Status: StatusUnreachable, Topology: &Topology{Nodes: []TopologyNode{{Address: "192.0.2.1"}, {Address: "1.1.1.1"}}}},
	}
	enrichTopologies(context.Background(), attempts, lookup)

	if len(lookup.calls) != 2 || lookup.calls["8.8.8.8"] != 1 || lookup.calls["1.1.1.1"] != 1 {
		t.Fatalf("calls = %#v", lookup.calls)
	}
	if attempts[0].Topology.Nodes[0].PublicIP || attempts[1].Topology.Nodes[0].PublicIP {
		t.Fatal("private or documentation address marked public")
	}
	node := attempts[0].Topology.Nodes[1]
	if !node.PublicIP || node.Geolocation == nil || node.Geolocation.City != "Seoul" || node.ASN == nil || node.ASN.Number != 15169 {
		t.Fatalf("node = %+v", node)
	}
}

func TestEnrichTopologiesCountsProviderFailures(t *testing.T) {
	attempts := []TraceAttempt{{Status: StatusUnreachable, Topology: &Topology{Nodes: []TopologyNode{{Address: "8.8.8.8"}, {Address: "1.1.1.1"}}}}}
	if failures := enrichTopologies(context.Background(), attempts, failingGeoIP{}); failures != 2 {
		t.Fatalf("failures = %d", failures)
	}
}

func TestPublicIPClassification(t *testing.T) {
	for _, address := range []string{"10.0.0.1", "100.64.0.1", "192.0.2.1", "203.0.113.1", "2001:db8::1", "64:ff9b::a00:1", "100::1", "::1"} {
		if isPublicIP(net.ParseIP(address)) {
			t.Errorf("%s classified as public", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(address)) {
			t.Errorf("%s classified as non-public", address)
		}
	}
}
