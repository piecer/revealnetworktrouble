package api

import (
	"context"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIPContextServiceFailureIsClosedInternalError(t *testing.T) {
	service := diagnostic.NewIPContextService(diagnostic.IPContextOptions{Resolver: &contextResolver{}, HTTP: func(context.Context, string) (int, []byte, error) { return 404, nil, nil }, Now: func() time.Time { panic("PRIVATE_SERVICE_CANARY") }})
	s, e := newServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{IPContext: service})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	newNetHTTPAdapter(s).ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(`{"address":"1.1.1.1"}`)))
	if w.Code != 500 || strings.Contains(w.Body.String(), "CANARY") || len(s.contextHandlers) != 0 || len(s.bodyDecodes) != 0 || len(s.responseWrites) != 0 {
		t.Fatalf("unsafe service fault status=%d body=%s", w.Code, w.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
