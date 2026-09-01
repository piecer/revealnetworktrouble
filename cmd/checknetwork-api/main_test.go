package main

import (
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/api"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func testEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLoadRuntimeConfigDefaultsToLoopbackTrustedLocal(t *testing.T) {
	config, err := loadRuntimeConfig(testEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.addr != "127.0.0.1:8080" || config.server.Mode != api.ModeTrustedLocal {
		t.Fatalf("config=%+v", config)
	}
	if config.writeTimeout < diagnostic.MaxRequestBudget || config.shutdownTimeout < diagnostic.MaxRequestBudget {
		t.Fatalf("timeouts cut off valid request: write=%v shutdown=%v budget=%v", config.writeTimeout, config.shutdownTimeout, diagnostic.MaxRequestBudget)
	}
}

func TestLoadRuntimeConfigRejectsIncompletePublicMode(t *testing.T) {
	for _, values := range []map[string]string{
		{"CHECKNETWORK_MODE": "public"},
		{"CHECKNETWORK_MODE": "public", "CHECKNETWORK_API_KEY": "secret"},
		{"CHECKNETWORK_MODE": "public", "CHECKNETWORK_API_KEY": "secret", "CHECKNETWORK_RATE_LIMIT_PER_MINUTE": "0"},
		{"CHECKNETWORK_MODE": "invalid"},
	} {
		if _, err := loadRuntimeConfig(testEnvironment(values)); err == nil {
			t.Fatalf("accepted environment: %+v", values)
		}
	}
}

func TestLoadRuntimeConfigAcceptsExplicitPublicMode(t *testing.T) {
	config, err := loadRuntimeConfig(testEnvironment(map[string]string{
		"CHECKNETWORK_MODE":                   "public",
		"CHECKNETWORK_API_KEY":                "secret",
		"CHECKNETWORK_RATE_LIMIT_PER_MINUTE":  "12",
		"CHECKNETWORK_MAX_CONCURRENT_REPORTS": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.server.Mode != api.ModePublic || config.server.RateLimitPerMinute != 12 || config.server.MaxConcurrentReports != 3 {
		t.Fatalf("server config=%+v", config.server)
	}
	if config.writeTimeout != diagnostic.MaxRequestBudget+5*time.Second {
		t.Fatalf("write timeout=%v", config.writeTimeout)
	}
}

func TestBuildCheckersAppliesPolicyOnlyInPublicMode(t *testing.T) {
	publicPolicy := diagnostic.NewNetworkPolicy(nil, nil)
	trusted := buildCheckers(api.ModeTrustedLocal, publicPolicy, nil)
	public := buildCheckers(api.ModePublic, publicPolicy, nil)
	if len(trusted) != len(public) || len(public) != 13 {
		t.Fatalf("trusted=%d public=%d", len(trusted), len(public))
	}
	assertPolicy := func(index int, checker diagnostic.Checker, want *diagnostic.NetworkPolicy) {
		t.Helper()
		var got *diagnostic.NetworkPolicy
		switch value := checker.(type) {
		case diagnostic.DNSChecker:
			got = value.Policy
		case diagnostic.TCPChecker:
			got = value.Policy
		case diagnostic.HTTPChecker:
			got = value.Policy
		case diagnostic.HTTPSChecker:
			got = value.Policy
		case diagnostic.TracerouteChecker:
			got = value.Policy
		case diagnostic.ServiceChecker:
			got = value.Policy
		default:
			t.Fatalf("checker %d has unexpected type %T", index, checker)
		}
		if got != want {
			t.Fatalf("checker %d policy=%p want %p", index, got, want)
		}
	}
	for index, checker := range trusted {
		assertPolicy(index, checker, nil)
	}
	for index, checker := range public {
		assertPolicy(index, checker, publicPolicy)
	}
}
