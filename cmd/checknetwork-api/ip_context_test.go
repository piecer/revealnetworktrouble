package main

import (
	"context"
	"errors"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

type contextShutdownResolver struct {
	entered chan struct{}
	release chan struct{}
}

func (r contextShutdownResolver) LookupAddr(context.Context, string) ([]string, error) {
	r.entered <- struct{}{}
	<-r.release
	return nil, nil
}
func (r contextShutdownResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, nil
}

type contextShutdownHTTP struct {
	service *diagnostic.IPContextService
	t       *testing.T
}

func (h contextShutdownHTTP) Shutdown(ctx context.Context) error {
	_, err := h.service.Lookup(ctx, "9.9.9.9", time.Now())
	if !errors.Is(err, diagnostic.ErrIPContextBusy) {
		h.t.Error("context admission remained open during HTTP drain")
	}
	return nil
}
func TestShutdownIncludesIPContextPrimitiveLifetime(t *testing.T) {
	resolver := contextShutdownResolver{make(chan struct{}, 4), make(chan struct{})}
	var once sync.Once
	defer once.Do(func() { close(resolver.release) })
	service := diagnostic.NewIPContextService(diagnostic.IPContextOptions{Resolver: resolver, HTTP: func(context.Context, string) (int, []byte, error) { return 404, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { service.Lookup(ctx, "1.1.1.1", time.Now()); close(done) }()
	<-resolver.entered
	cancel()
	<-done
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err = shutdownService(context.Background(), 20*time.Millisecond, newOperationalState(true), contextShutdownHTTP{service, t}, supervisor, logger, service)
	if err == nil {
		t.Error("incomplete primitive shutdown claimed complete")
	}
	once.Do(func() { close(resolver.release) })
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err = service.Close(ctx2); err != nil {
		t.Fatal(err)
	}
}
