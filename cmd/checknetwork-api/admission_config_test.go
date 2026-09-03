package main

import (
	"strconv"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/api"
)

func TestRuntimeConnectionAdmissionConfigurationDefaultsAndHardMaximum(t *testing.T) {
	defaults, err := loadRuntimeConfig(testEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.maxConnections != defaultMaxConnections || defaultMaxConnections != 128 || maxConnectionsLimit != 256 {
		t.Fatalf("connection defaults=%d/%d hard=%d", defaults.maxConnections, defaultMaxConnections, maxConnectionsLimit)
	}
	for _, capacity := range []int{1, maxConnectionsLimit} {
		config, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_MAX_CONNECTIONS": strconv.Itoa(capacity)}))
		if err != nil || config.maxConnections != capacity {
			t.Fatalf("capacity=%d config=%+v err=%v", capacity, config, err)
		}
	}
	for _, value := range []string{"0", "-1", "invalid", strconv.Itoa(maxConnectionsLimit + 1)} {
		if _, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_MAX_CONNECTIONS": value})); err == nil {
			t.Fatalf("accepted connection capacity %q", value)
		}
	}
}

func TestRuntimeBodyDecodeAdmissionConfigurationDefaultsToEffectiveReportLimitAndBounds64(t *testing.T) {
	defaults, err := loadRuntimeConfig(testEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.server.MaxConcurrentBodyDecodes != 0 {
		t.Fatalf("zero report/body defaults should defer together to API, got body=%d", defaults.server.MaxConcurrentBodyDecodes)
	}
	configured, err := loadRuntimeConfig(testEnvironment(map[string]string{
		"CHECKNETWORK_MAX_CONCURRENT_REPORTS":      "3",
		"CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES": "5",
	}))
	if err != nil || configured.server.MaxConcurrentBodyDecodes != 5 {
		t.Fatalf("configured body decode capacity=%d err=%v", configured.server.MaxConcurrentBodyDecodes, err)
	}
	for _, value := range []string{"0", "-1", "invalid", strconv.Itoa(api.MaxConcurrentBodyDecodesLimit + 1)} {
		if _, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES": value})); err == nil {
			t.Fatalf("accepted body decode capacity %q", value)
		}
	}
}
