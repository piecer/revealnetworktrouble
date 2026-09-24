package diagnostic

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestGeoIPCoordinatesRequireObservedPair(t *testing.T) {
	for _, coordinates := range []string{"", `,"latitude":null,"longitude":null`, `,"latitude":1`, `,"longitude":2`, `,"latitude":null,"longitude":2`} {
		t.Run(coordinates, func(t *testing.T) {
			calls := 0
			lookup := NewIPWhoIsLookup(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return geoIPResponse(`{"success":true,"connection":{"asn":15169}` + coordinates + `}`), nil
			})}, "https://geo.example/")
			for i := 0; i < 2; i++ {
				metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
				if err != nil || metadata.Geolocation != nil || metadata.ASN == nil || metadata.ASN.Number != 15169 {
					t.Fatalf("missing coordinate was invented or ASN lost: metadata=%+v err=%v", metadata, err)
				}
				if i == 1 && metadata.Source != GeoIPSourceCache {
					t.Fatalf("valid ASN-only observation not cached: %+v", metadata)
				}
			}
			if calls != 1 {
				t.Fatalf("provider calls=%d, want one cached ASN-only lookup", calls)
			}
		})
	}
}

func TestGeoIPNoUsableObservationsAreNotSuccessCached(t *testing.T) {
	for _, body := range []string{`{"success":true}`, `{"success":true,"latitude":null,"longitude":null}`, `{"success":true,"connection":{"asn":0}}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			lookup := NewIPWhoIsLookup(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return geoIPResponse(body), nil
			})}, "https://geo.example/")
			for i := 0; i < 2; i++ {
				_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
				requireGeoIPError(t, err, GeoIPErrorMalformed, false)
			}
			if calls != 2 || len(lookup.cache) != 0 {
				t.Fatalf("empty observations cached: calls=%d cache=%d", calls, len(lookup.cache))
			}
		})
	}
}

func TestGeoIPExplicitZeroCoordinatesRemainObserved(t *testing.T) {
	metadata, err := decodeGeoIPResponse(strings.NewReader(`{"success":true,"latitude":0,"longitude":0}`))
	if err != nil || metadata.Geolocation == nil || metadata.Geolocation.Latitude != 0 || metadata.Geolocation.Longitude != 0 {
		t.Fatalf("explicit zero coordinates lost: metadata=%+v err=%v", metadata, err)
	}
}
