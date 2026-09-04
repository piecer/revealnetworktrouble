package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

var (
	version  = "dev"
	revision = "dev"
)

const (
	headerTimeout         = 5 * time.Second
	bodyReadAllowance     = 30 * time.Second
	executionTimeout      = diagnostic.MaxRequestBudget
	responseGrace         = 5 * time.Second
	shutdownMargin        = 5 * time.Second
	readTimeout           = headerTimeout + bodyReadAllowance
	writeTimeout          = bodyReadAllowance + executionTimeout + responseGrace
	shutdownTimeout       = headerTimeout + bodyReadAllowance + executionTimeout + responseGrace + shutdownMargin
	normalClientTimeout   = 315 * time.Second
	maxHeaderBytes        = 64 * 1024
	defaultMaxConnections = 128
	maxConnectionsLimit   = 256
)

type runtimeConfig struct {
	addr                string
	geoIPURL            string
	server              api.ServerConfig
	maxConcurrentChecks int
	maxConnections      int
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
	config.server.Revision = revision
	tracerouteCapability, _ := diagnostic.ProbeTracerouteCapability()
	operational := newOperationalState(tracerouteCapability != nil)
	var policy *diagnostic.NetworkPolicy
	if config.server.Mode == api.ModePublic {
		policy = diagnostic.NewNetworkPolicy(nil, nil)
	}
	geoIP := diagnostic.NewIPWhoIsLookupWithPolicy(nil, config.geoIPURL, policy)
	checkers := buildCheckers(config.server.Mode, policy, geoIP, tracerouteCapability)
	supervisor, err := diagnostic.NewCheckerSupervisor(config.maxConcurrentChecks)
	if err != nil {
		logger.Error("invalid checker supervisor configuration", "error", err)
		return 1
	}
	runner := newProductionRunner(supervisor, checkers...)
	config.server.DrainingProvider = operational
	businessHandler, err := api.NewServerWithConfig(runner, logger, version, config.server)
	if err != nil {
		logger.Error("invalid server configuration", "error", err)
		return 1
	}
	handler := newOperationalHandler(operational, businessHandler)
	server := newHTTPServer(config.addr, handler)
	rawListener, err := net.Listen("tcp", config.addr)
	if err != nil {
		logger.Error("server listen failed", "error", err)
		return 1
	}
	listener, err := newBoundedListener(rawListener, config.maxConnections)
	if err != nil {
		_ = rawListener.Close()
		logger.Error("invalid connection admission configuration", "error", err)
		return 1
	}
	operational.MarkAccepting()

	serveErr := make(chan error, 1)
	go func() {
		logServerStarted(logger, listener.Addr().String(), version, revision)
		if err := serveHTTP(server, listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	shutdownErr := shutdownService(context.Background(), config.shutdownTimeout, operational, server, supervisor, logger)
	return finishRun(exitCode, shutdownErr, logger)
}

func finishRun(exitCode int, shutdownErr error, logger *slog.Logger) int {
	if shutdownErr != nil {
		return 1
	}
	logger.Info("server stopped")
	return exitCode
}

func logServerStarted(logger *slog.Logger, address, buildVersion, buildRevision string) {
	logger.Info("server started", "address", address, "version", buildVersion, "revision", buildRevision)
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

var errCheckerDrainIncomplete = errors.New("checker drain incomplete")

const shutdownFailureReason = "shutdown_incomplete"

// shutdownService drains HTTP first, then always stops checker admission. Both
// phases share one end-to-end deadline so the process drain stays within the
// container stop grace period even when HTTP consumes the entire budget.
func shutdownService(parent context.Context, timeout time.Duration, operational *operationalState, server shutdownHTTPServer, supervisor *diagnostic.CheckerSupervisor, logger *slog.Logger) error {
	operational.BeginDrain()
	drainCtx, cancelDrain := context.WithTimeout(parent, timeout)
	defer cancelDrain()
	httpErr := server.Shutdown(drainCtx)
	snapshot := supervisor.ShutdownSnapshot(drainCtx)
	remaining := snapshot.Active
	fields := []any{"active", snapshot.Active, "stuck", snapshot.Stuck, "remaining", remaining}
	var checkerErr error
	if remaining != 0 || snapshot.Active != 0 || snapshot.Stuck != 0 {
		checkerErr = errCheckerDrainIncomplete
	}
	shutdownErr := errors.Join(httpErr, checkerErr)
	if shutdownErr != nil {
		logger.Error("service shutdown failed", append([]any{"reason", shutdownFailureReason}, fields...)...)
	} else {
		logger.Info("service shutdown completed", fields...)
	}
	return shutdownErr
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
	maxBodyDecodes, err := optionalPositiveInt(lookup("CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES"), "CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES")
	if err != nil {
		return runtimeConfig{}, err
	}
	if maxBodyDecodes > api.MaxConcurrentBodyDecodesLimit {
		return runtimeConfig{}, fmt.Errorf("CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES must not exceed %d", api.MaxConcurrentBodyDecodesLimit)
	}
	rateLimit, err := optionalPositiveInt(lookup("CHECKNETWORK_RATE_LIMIT_PER_MINUTE"), "CHECKNETWORK_RATE_LIMIT_PER_MINUTE")
	if err != nil {
		return runtimeConfig{}, err
	}
	maxConnections := defaultMaxConnections
	if value := strings.TrimSpace(lookup("CHECKNETWORK_MAX_CONNECTIONS")); value != "" {
		maxConnections, err = optionalPositiveInt(value, "CHECKNETWORK_MAX_CONNECTIONS")
		if err != nil {
			return runtimeConfig{}, err
		}
		if maxConnections > maxConnectionsLimit {
			return runtimeConfig{}, fmt.Errorf("CHECKNETWORK_MAX_CONNECTIONS must not exceed %d", maxConnectionsLimit)
		}
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
		maxConnections:      maxConnections,
		server: api.ServerConfig{
			AllowedOrigins:           splitCSV(envFrom(lookup, "CHECKNETWORK_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
			MaxConcurrentReports:     maxConcurrent,
			MaxConcurrentBodyDecodes: maxBodyDecodes,
			Mode:                     mode,
			APIKey:                   strings.TrimSpace(lookup("CHECKNETWORK_API_KEY")),
			RateLimitPerMinute:       rateLimit,
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

func buildCheckers(mode api.DeploymentMode, policy *diagnostic.NetworkPolicy, geoIP diagnostic.GeoIPLookup, tracerouteCapability *diagnostic.TracerouteCapability) []diagnostic.Checker {
	if mode != api.ModePublic {
		policy = nil
	}
	checkers := []diagnostic.Checker{
		diagnostic.DNSChecker{Policy: policy},
		diagnostic.TCPChecker{Policy: policy},
		diagnostic.HTTPChecker{Policy: policy},
		diagnostic.HTTPSChecker{Policy: policy},
		diagnostic.NewTracerouteChecker(tracerouteCapability, geoIP, policy),
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
