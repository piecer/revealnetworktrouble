package diagnostic

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestIPContextReverseRejectsNonASCIIBeforeCaseMapping(t *testing.T) {
	s := contextTestService(contextTestResolver{reverse: func(context.Context, string) ([]string, error) { return []string{"K.example"}, nil }, forward: func(context.Context, string) ([]net.IPAddr, error) {
		t.Error("non-ASCII name was queried")
		return nil, nil
	}})
	v, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if err != nil || v.ReverseDNS.Status != "invalid_response" {
		t.Fatalf("got %+v %v", v, err)
	}
}
