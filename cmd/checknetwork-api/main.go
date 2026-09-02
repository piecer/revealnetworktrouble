package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/api"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

var version = "dev"

const (
	headerTimeout       = 5 * time.Second
	bodyReadAllowance   = 30 * time.Second
	executionTimeout    = diagnostic.MaxRequestBudget
	responseGrace       = 5 * time.Second
	shutdownMargin      = 5 * time.Second
	readTimeout         = headerTimeout + bodyReadAllowance
	writeTimeout        = bodyReadAllowance + executionTimeout + responseGrace
	shutdownTimeout     = headerTimeout + bodyReadAllowance + executionTimeout + responseGrace + shutdownMargin
	normalClientTimeout = 315 * time.Second
	maxHeaderBytes      = 64 * 1024
)

type runtimeConfig struct {
	addr                string
	geoIPURL            string
	server              api.ServerConfig
	maxConcurrentChecks int
	writeTimeout        time.Duration
	shutdownTimeout     time.Duration
}

func main() {
	os.Exit(runMain())
}

func runMain() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := loadRuntimeConfig(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 1
	}
	var policy *diagnostic.NetworkPolicy
	if config.server.Mode == api.ModePublic {
		policy = diagnostic.NewNetworkPolicy(nil, nil)
	}
	geoIP := diagnostic.NewIPWhoIsLookupWithPolicy(nil, config.geoIPURL, policy)
	checkers := buildCheckers(config.server.Mode, policy, geoIP)
	supervisor, err := diagnostic.NewCheckerSupervisor(config.maxConcurrentChecks)
	if err != nil {
		logger.Error("invalid checker supervisor configuration", "error", err)
		return 1
	}
	runner := newProductionRunner(supervisor, checkers...)
	handler, err := api.NewServerWithConfig(runner, logger, version, config.server)
	if err != nil {
		logger.Error("invalid server configuration", "error", err)
		return 1
	}
	server := newHTTPServer(config.addr, handler)

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", server.Addr, "version", version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	exitCode := 0
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			logger.Error("server stopped unexpectedly", "error", err)
			exitCode = 1
		}
	}
	if err := shutdownService(context.Background(), config.shutdownTimeout, server, supervisor, logger); err != nil {
		exitCode = 1
	}
	logger.Info("server stopped")
	return exitCode
}

func newProductionRunner(supervisor *diagnostic.CheckerSupervisor, checkers ...diagnostic.Checker) *diagnostic.Runner {
	return diagnostic.NewRunnerWithSupervisor(supervisor, checkers...)
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: headerTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

type shutdownHTTPServer interface {
	Shutdown(context.Context) error
}

// shutdownService drains HTTP first, then always stops checker admission. Both
// phases share one end-to-end deadline so the process drain stays within the
// container stop grace period even when HTTP consumes the entire budget.
func shutdownService(parent context.Context, timeout time.Duration, server shutdownHTTPServer, supervisor *diagnostic.CheckerSupervisor, logger *slog.Logger) error {
	drainCtx, cancelDrain := context.WithTimeout(parent, timeout)
	defer cancelDrain()
	httpErr := server.Shutdown(drainCtx)
	remaining := supervisor.Shutdown(drainCtx)
	snapshot := supervisor.Snapshot()
	fields := []any{"active", snapshot.Active, "stuck", snapshot.Stuck, "remaining", remaining}
	if httpErr != nil {
		logger.Error("service shutdown completed with HTTP drain failure", append([]any{"error", httpErr}, fields...)...)
	} else {
		logger.Info("service shutdown completed", fields...)
	}
	return httpErr
}

func loadRuntimeConfig(lookup func(string) string) (runtimeConfig, error) {
	mode := api.DeploymentMode(envFrom(lookup, "CHECKNETWORK_MODE", string(api.ModeTrustedLocal)))
	if mode != api.ModeTrustedLocal && mode != api.ModePublic {
		return runtimeConfig{}, fmt.Errorf("CHECKNETWORK_MODE must be trusted-local or public")
	}
	maxConcurrent, err := optionalPositiveInt(lookup("CHECKNETWORK_MAX_CONCURRENT_REPORTS"), "CHECKNETWORK_MAX_CONCURRENT_REPORTS")
	if err != nil {
		return runtimeConfig{}, err
	}
	if maxConcurrent > api.MaxConcurrentReportsLimit {
		return runtimeConfig{}, fmt.Errorf("CHECKNETWORK_MAX_CONCURRENT_REPORTS must not exceed %d", api.MaxConcurrentReportsLimit)
	}
	rateLimit, err := optionalPositiveInt(lookup("CHECKNETWORK_RATE_LIMIT_PER_MINUTE"), "CHECKNETWORK_RATE_LIMIT_PER_MINUTE")
	if err != nil {
		return runtimeConfig{}, err
	}
	maxConcurrentChecks := diagnostic.DefaultCheckerCapacity
	if value := strings.TrimSpace(lookup("CHECKNETWORK_MAX_CONCURRENT_CHECKS")); value != "" {
		maxConcurrentChecks, err = optionalPositiveInt(value, "CHECKNETWORK_MAX_CONCURRENT_CHECKS")
		if err != nil {
			return runtimeConfig{}, err
		}
		if maxConcurrentChecks > diagnostic.MaxCheckerCapacity {
			return runtimeConfig{}, fmt.Errorf("CHECKNETWORK_MAX_CONCURRENT_CHECKS must not exceed %d", diagnostic.MaxCheckerCapacity)
		}
	}
	config := runtimeConfig{
		addr:                envFrom(lookup, "CHECKNETWORK_ADDR", "127.0.0.1:8080"),
		geoIPURL:            envFrom(lookup, "CHECKNETWORK_GEOIP_URL", "https://ipwho.is/"),
		maxConcurrentChecks: maxConcurrentChecks,
		server: api.ServerConfig{
			AllowedOrigins:       splitCSV(envFrom(lookup, "CHECKNETWORK_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
			MaxConcurrentReports: maxConcurrent,
			Mode:                 mode,
			APIKey:               strings.TrimSpace(lookup("CHECKNETWORK_API_KEY")),
			RateLimitPerMinute:   rateLimit,
		},
		writeTimeout:    writeTimeout,
		shutdownTimeout: shutdownTimeout,
	}
	if mode == api.ModePublic {
		if config.server.APIKey == "" {
			return runtimeConfig{}, fmt.Errorf("public mode requires CHECKNETWORK_API_KEY")
		}
		if config.server.RateLimitPerMinute <= 0 {
			return runtimeConfig{}, fmt.Errorf("public mode requires CHECKNETWORK_RATE_LIMIT_PER_MINUTE")
		}
	}
	return config, nil
}

func buildCheckers(mode api.DeploymentMode, policy *diagnostic.NetworkPolicy, geoIP diagnostic.GeoIPLookup) []diagnostic.Checker {
	if mode != api.ModePublic {
		policy = nil
	}
	checkers := []diagnostic.Checker{
		diagnostic.DNSChecker{Policy: policy},
		diagnostic.TCPChecker{Policy: policy},
		diagnostic.HTTPChecker{Policy: policy},
		diagnostic.HTTPSChecker{Policy: policy},
		diagnostic.TracerouteChecker{GeoIP: geoIP, Policy: policy},
	}
	for _, checker := range diagnostic.DefaultServiceCheckers() {
		service := checker.(diagnostic.ServiceChecker)
		service.Policy = policy
		checkers = append(checkers, service)
	}
	return checkers
}

func optionalPositiveInt(value, name string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func envFrom(lookup func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(lookup(name)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
