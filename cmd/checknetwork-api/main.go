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

type runtimeConfig struct {
	addr            string
	geoIPURL        string
	server          api.ServerConfig
	writeTimeout    time.Duration
	shutdownTimeout time.Duration
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := loadRuntimeConfig(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	var policy *diagnostic.NetworkPolicy
	if config.server.Mode == api.ModePublic {
		policy = diagnostic.NewNetworkPolicy(nil, nil)
	}
	geoIP := diagnostic.NewIPWhoIsLookupWithPolicy(nil, config.geoIPURL, policy)
	checkers := buildCheckers(config.server.Mode, policy, geoIP)
	runner := diagnostic.NewRunner(checkers...)
	handler, err := api.NewServerWithConfig(runner, logger, version, config.server)
	if err != nil {
		logger.Error("invalid server configuration", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              config.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      config.writeTimeout,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("server started", "address", server.Addr, "version", version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
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
	rateLimit, err := optionalPositiveInt(lookup("CHECKNETWORK_RATE_LIMIT_PER_MINUTE"), "CHECKNETWORK_RATE_LIMIT_PER_MINUTE")
	if err != nil {
		return runtimeConfig{}, err
	}
	config := runtimeConfig{
		addr:     envFrom(lookup, "CHECKNETWORK_ADDR", "127.0.0.1:8080"),
		geoIPURL: envFrom(lookup, "CHECKNETWORK_GEOIP_URL", "https://ipwho.is/"),
		server: api.ServerConfig{
			AllowedOrigins:       splitCSV(envFrom(lookup, "CHECKNETWORK_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
			MaxConcurrentReports: maxConcurrent,
			Mode:                 mode,
			APIKey:               strings.TrimSpace(lookup("CHECKNETWORK_API_KEY")),
			RateLimitPerMinute:   rateLimit,
		},
		writeTimeout:    diagnostic.MaxRequestBudget + 5*time.Second,
		shutdownTimeout: diagnostic.MaxRequestBudget + 5*time.Second,
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
