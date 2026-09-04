//go:build !windows

package main

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/api"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestLiveGNUInetutilsReadinessAndTracerouteShareWorkingCapability(t *testing.T) {
	executable, err := exec.LookPath("traceroute")
	if err != nil {
		t.Skip("traceroute is not installed")
	}
	versionOutput, err := exec.Command(executable, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOutput), "GNU inetutils") {
		t.Skip("installed traceroute is not GNU inetutils")
	}

	capability, err := diagnostic.ProbeTracerouteCapability()
	if err != nil {
		t.Fatalf("GNU inetutils capability probe failed: %v", err)
	}
	state := newOperationalState(capability != nil)
	state.MarkAccepting()
	ready := operationalRequest(t, newOperationalHandler(state, http.NotFoundHandler()), http.MethodGet, "/readyz")
	if ready.Code != http.StatusOK || ready.Body.String() != readyBody {
		t.Fatalf("readiness status=%d body=%q", ready.Code, ready.Body.String())
	}

	var traceroute diagnostic.TracerouteChecker
	for _, checker := range buildCheckers(api.ModeTrustedLocal, nil, nil, capability) {
		if candidate, ok := checker.(diagnostic.TracerouteChecker); ok {
			traceroute = candidate
			break
		}
	}
	result := traceroute.Check(context.Background(), diagnostic.Target{
		Kind: diagnostic.KindTraceroute, Address: "127.0.0.1", Attempts: 1,
	})
	if result.Status != diagnostic.StatusHealthy {
		t.Fatalf("live GNU inetutils loopback result=%+v", result)
	}
}
