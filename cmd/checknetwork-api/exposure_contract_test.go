package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
)

func TestLoadRuntimeConfigRejectsTrustedLocalNonLoopbackBinding(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.168.1.5:8080", "10.0.0.2:8080", "localhost:8080", "[::ffff:10.0.0.2]:8080", "[fe80::1%eth0]:8080", "[fd00::1]:8080", "[2001:4860:4860::8888]:8080", "8080"} {
		_, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_ADDR": addr}))
		if err == nil {
			t.Fatalf("addr %q: expected rejection without explicit acknowledgement", addr)
		}
		message := err.Error()
		for _, required := range []string{`CHECKNETWORK_EXPOSE_TRUSTED_LOCAL`, "public mode", `CHECKNETWORK_API_KEY`} {
			if !strings.Contains(message, required) {
				t.Errorf("addr %q: error must mention %q, got: %s", addr, required, message)
			}
		}
	}
}

func TestLoadRuntimeConfigTrustedLocalAcknowledgementIsExactYesOnly(t *testing.T) {
	for _, value := range []string{"", "yea", "YES", "true", "no", "1", " yes", "yes ", "	yes\n", "\u00a0yes", "ｙｅｓ"} {
		if _, err := loadRuntimeConfig(testEnvironment(map[string]string{
			"CHECKNETWORK_ADDR":                 ":8080",
			"CHECKNETWORK_EXPOSE_TRUSTED_LOCAL": value,
		})); err == nil {
			t.Fatalf("value %q: acknowledgement must be exactly \"yes\"", value)
		}
	}
	config, err := loadRuntimeConfig(testEnvironment(map[string]string{
		"CHECKNETWORK_ADDR":                 ":8080",
		"CHECKNETWORK_EXPOSE_TRUSTED_LOCAL": "yes",
	}))
	if err != nil {
		t.Fatalf("value \"yes\" must be accepted: %v", err)
	}
	if !config.exposedTrustedLocal {
		t.Fatalf("acknowledged exposure must be flagged for the startup warning: %+v", config)
	}
}

func TestLoadRuntimeConfigLoopbackBindingsNeedNoAcknowledgement(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "127.12.34.56:8080", "[::1]:8080", "[::ffff:127.0.0.1]:8080"} {
		config, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_ADDR": addr}))
		if err != nil {
			t.Fatalf("addr %q: loopback must not require acknowledgement: %v", addr, err)
		}
		if config.exposedTrustedLocal {
			t.Errorf("addr %q: loopback bind must not be flagged as exposed", addr)
		}
	}
}

func TestPublicModeNonLoopbackWithoutCredentialsStillRejected(t *testing.T) {
	for _, ack := range []string{"", "yes"} {
		values := map[string]string{
			"CHECKNETWORK_MODE":                 "public",
			"CHECKNETWORK_ADDR":                 ":8080",
			"CHECKNETWORK_EXPOSE_TRUSTED_LOCAL": ack,
		}
		if _, err := loadRuntimeConfig(testEnvironment(values)); err == nil || !strings.Contains(err.Error(), "CHECKNETWORK_API_KEY") {
			t.Fatalf("ack=%q: missing public credential must fail: %v", ack, err)
		}
		values["CHECKNETWORK_API_KEY"] = "synthetic-config-test-key"
		if _, err := loadRuntimeConfig(testEnvironment(values)); err == nil || !strings.Contains(err.Error(), "CHECKNETWORK_RATE_LIMIT_PER_MINUTE") {
			t.Fatalf("ack=%q: missing public rate limit must fail: %v", ack, err)
		}
		values["CHECKNETWORK_RATE_LIMIT_PER_MINUTE"] = "1"
		config, err := loadRuntimeConfig(testEnvironment(values))
		if err != nil || config.exposedTrustedLocal {
			t.Fatalf("ack=%q: valid public mode must not need or emit trusted-local approval: %v", ack, err)
		}
	}
}

func TestWarnExposedTrustedLocalEmitsSingleStableRecord(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	warnExposedTrustedLocal(logger, "0.0.0.0:8080")
	records := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(records) != 1 {
		t.Fatalf("warning must emit exactly one record, got %d", len(records))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(records[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["level"] != "WARN" || entry["address"] != "0.0.0.0:8080" {
		t.Fatalf("record=%v", entry)
	}
	for _, forbidden := range []string{"Bearer ", "Authorization", "api_key"} {
		if strings.Contains(records[0], forbidden) {
			t.Errorf("warning leaked credential material: %s", records[0])
		}
	}
}

func TestReleaseSmokeEnvironmentAcceptsExposureGuard(t *testing.T) {
	// Exercise the actual shell helper against the existing fake Docker, not a
	// copied environment or a substring assertion; no live daemon is modified.
	command := exec.Command("python3", "-c", `
import json
from verify_release_api_resource_ownership_test import run_case
result, state, _ = run_case("container", "success")
assert result.returncode == 0, result.stdout
print(json.dumps([c for c in state["calls"] if c[:2] == ["container", "create"]]))
`)
	command.Dir = "../../scripts"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke invocation: %v\n%s", err, output)
	}
	var calls [][]string
	if err := json.Unmarshal(output, &calls); err != nil || len(calls) != 1 {
		t.Fatalf("expected one smoke create: %v\n%s", err, output)
	}
	values := map[string]string{}
	for i, arg := range calls[0] {
		if arg == "--env" && i+1 < len(calls[0]) {
			name, value, ok := strings.Cut(calls[0][i+1], "=")
			if ok {
				values[name] = value
			}
		}
	}
	config, err := loadRuntimeConfig(testEnvironment(values))
	if err != nil {
		t.Fatalf("release smoke cannot start with its actual environment: %v", err)
	}
	if values["CHECKNETWORK_MODE"] != "trusted-local" || config.addr != ":8080" || !config.exposedTrustedLocal {
		t.Fatal("release smoke must explicitly select and acknowledge container-local trusted mode")
	}
}

func TestComposeResolvedExposureBoundary(t *testing.T) {
	source := repositoryFile(t, "compose.yaml")

	// Configuration resolution only, in pure Go: the default suite enforces this
	// boundary without the Docker CLI, which is cross-checked when Docker is present.
	resolve := func(env map[string]string, profiles ...string) map[string]resolvedComposeService {
		t.Helper()
		resolved, err := resolveComposeConfig(source, env, profiles)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}

	// Single-port serving is the default: one service publishes exactly one loopback port and
	// serves both the Web workspace and the API from that same origin.
	defaults := resolve(nil)
	if len(defaults) != 1 {
		t.Fatalf("default Compose must resolve to one service for single-port serving, got %v", sortedComposeNames(defaults))
	}
	api := defaults["api"]
	if len(api.Ports) != 1 || api.Ports[0].HostIP != "127.0.0.1" || api.Ports[0].Published != "8090" || api.Ports[0].Target != "8080" {
		t.Fatalf("single-port default must retain one loopback-only host publication: %+v", api.Ports)
	}
	// The resolved environment is the contract itself, never an ambient .env file.
	if len(api.Environment) == 0 {
		t.Fatal("default Compose does not resolve the documented environment contract")
	}
	config, err := loadRuntimeConfig(testEnvironment(api.Environment))
	if err != nil || config.addr != ":8080" || !config.exposedTrustedLocal {
		t.Fatalf("default Compose must acknowledge its container bind: %v", err)
	}
	if !config.webUI {
		t.Fatalf("single-port default must serve the embedded Web workspace on the API listener: %v", api.Environment)
	}
	if strings.Join(config.server.AllowedOrigins, ",") != "http://localhost:8090,http://127.0.0.1:8090" {
		t.Fatalf("default Compose origin contract changed: %v", config.server.AllowedOrigins)
	}
	healthcheck := strings.Join(api.Healthcheck, " ")
	if healthcheck == "" || strings.Contains(healthcheck, "\n") || !strings.Contains(healthcheck, "http://127.0.0.1:8080/readyz") {
		t.Fatalf("healthcheck must stay a single-line exec form on the loopback container address: %q", api.Healthcheck)
	}

	// The two-port layout remains available as an explicit profile with its own loopback publication.
	profiled := resolve(nil, "standalone-web")
	if len(profiled) != 2 {
		t.Fatalf("standalone-web profile must add the static Web service, got %v", sortedComposeNames(profiled))
	}
	for _, name := range []string{"api", "web"} {
		ports := profiled[name].Ports
		if len(ports) != 1 || ports[0].HostIP != "127.0.0.1" {
			t.Errorf("%s must retain one loopback-only host publication: %+v", name, ports)
		}
	}

	// A caller may intentionally replace the default origin, but the default itself
	// stays loopback-only.
	overridden := resolve(map[string]string{"CHECKNETWORK_ALLOWED_ORIGINS": "http://10.0.0.5:8090"})
	if overridden["api"].Environment["CHECKNETWORK_ALLOWED_ORIGINS"] != "http://10.0.0.5:8090" {
		t.Fatalf("caller origin override did not resolve: %v", overridden["api"].Environment)
	}
	overriddenConfig, err := loadRuntimeConfig(testEnvironment(overridden["api"].Environment))
	if err != nil || strings.Join(overriddenConfig.server.AllowedOrigins, ",") != "http://10.0.0.5:8090" {
		t.Fatalf("caller override did not reach the runtime contract: %v", err)
	}
}
