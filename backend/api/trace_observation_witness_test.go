package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type traceObservationResolver func(context.Context, string) ([]net.IPAddr, error)

func (resolve traceObservationResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return resolve(ctx, host)
}

type traceObservationTransport func(*http.Request) (*http.Response, error)

func (transport traceObservationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestTraceObservationProducerWireWitnesses(t *testing.T) {
	for _, scenario := range []string{"healthy-varied-paths", "later-DNS-failure", "slow-optional-GeoIP", "consecutive-RTT-jumps"} {
		t.Run(scenario, func(t *testing.T) {
			attemptCount, wantReached, wantRoutes := 2, 2, 2
			wantStatus := diagnostic.StatusHealthy
			var wantFinding diagnostic.FindingCode
			var geoIP diagnostic.GeoIPLookup
			var policy *diagnostic.NetworkPolicy
			resolves, commands := 0, 0
			switch scenario {
			case "healthy-varied-paths":
				wantFinding = diagnostic.FindingTraceroutePathUnstable
			case "later-DNS-failure":
				wantReached, wantRoutes, wantStatus = 1, 1, diagnostic.StatusDegraded
				wantFinding = diagnostic.FindingTracerouteExecutionFailed
				policy = diagnostic.NewNetworkPolicy(traceObservationResolver(func(context.Context, string) ([]net.IPAddr, error) {
					resolves++
					if resolves > 1 {
						return nil, errors.New("resolution failed")
					}
					return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
				}), nil)
			case "slow-optional-GeoIP":
				attemptCount, wantReached, wantRoutes = 1, 1, 1
				geoIP = diagnostic.NewIPWhoIsLookup(&http.Client{Transport: traceObservationTransport(func(req *http.Request) (*http.Response, error) {
					<-req.Context().Done()
					return nil, req.Context().Err()
				})}, "https://geo.invalid/")
			case "consecutive-RTT-jumps":
				attemptCount, wantReached, wantRoutes = 1, 1, 1
				wantStatus, wantFinding = diagnostic.StatusDegraded, diagnostic.FindingTraceroutePathDegraded
			}
			checker := diagnostic.NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				commands++
				hops := "1  8.8.8.8  5.0 ms\n"
				if scenario == "healthy-varied-paths" {
					hop := "1.1.1.1"
					if commands == 2 {
						hop = "9.9.9.9"
					}
					hops = fmt.Sprintf("1  %s  1.0 ms\n2  8.8.8.8  5.0 ms\n", hop)
				} else if scenario == "consecutive-RTT-jumps" {
					hops = "1  1.1.1.1  1 ms\n2  9.9.9.9  101 ms\n3  8.8.8.8  201 ms\n"
				}
				return []byte("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n" + hops), nil
			}, geoIP, policy)
			runner := diagnostic.NewRunnerWithSupervisor(mustAPICheckerSupervisor(t, 1), checker)
			report, err := runner.RunWithID(context.Background(), "cdcdcdcdcdcdcdcdcdcdcdcd", diagnostic.Request{
				TimeoutMS: 100, Targets: []diagnostic.Target{{Kind: diagnostic.KindTraceroute, Address: "route.example", Attempts: attemptCount}},
			})
			if err != nil {
				t.Fatal(err)
			}
			result := report.Results[0]
			if result.Status != wantStatus || result.ErrorCode != "" || result.Details["attempts_reached"] != wantReached || commands != wantReached {
				t.Fatalf("unexpected producer witness: result=%+v commands=%d", result, commands)
			}
			if scenario == "later-DNS-failure" {
				attempts := result.Details["attempts"].([]diagnostic.TraceAttempt)
				if resolves != 2 || attempts[1].ErrorCode != "traceroute_failed" || attempts[1].Topology != nil {
					t.Fatalf("pre-command failed attempt=%+v resolves=%d", attempts, resolves)
				}
			}
			normalizeFixtureReport(&report, 10)
			full, err := marshalFullResponse(report)
			if err != nil {
				t.Fatal(err)
			}
			compact, err := marshalCompactResponse(report)
			if err != nil {
				t.Fatal(err)
			}
			assertTracerouteWitnessWireTruth(t, full, compact, tracerouteWitness{wantCompactRoutes: wantRoutes})
			for mode, payload := range map[string][]byte{"full": full, "compact": compact} {
				path := "../../testdata/trace-observation-" + scenario + "-" + mode + ".json"
				if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
					if err := os.WriteFile(path, payload, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				committed, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(committed, payload) {
					t.Fatalf("%s %s differs from producer bytes", scenario, mode)
				}
				var decoded diagnostic.Report
				if err := json.Unmarshal(payload, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Status != wantStatus || decoded.Results[0].Status != wantStatus {
					t.Fatalf("%s changed aggregate health", mode)
				}
				if wantFinding != "" {
					if len(decoded.Analysis.Findings) != 1 || decoded.Analysis.Findings[0].Code != wantFinding {
						t.Fatalf("%s findings=%+v want %s", mode, decoded.Analysis.Findings, wantFinding)
					}
				} else if scenario == "slow-optional-GeoIP" {
					coverage := decoded.Analysis.Coverage.Enrichment
					if len(coverage) != 1 || len(coverage[0].Failures) != 1 || coverage[0].Failures[0].Kind != diagnostic.GeoIPErrorTimeout {
						t.Fatalf("%s lost typed optional failure: %+v", mode, coverage)
					}
				}
				if len(decoded.Analysis.Coverage.Limitations) != 0 {
					t.Fatalf("%s producer contradicted analysis: %+v", mode, decoded.Analysis.Coverage)
				}
			}
		})
	}
}

func TestTraceObservationPublicRebindingRemainsPrivacySafePolicyError(t *testing.T) {
	for _, mode := range []string{"full", "compact"} {
		t.Run(mode, func(t *testing.T) {
			resolves, commands := 0, 0
			policy := diagnostic.NewNetworkPolicy(traceObservationResolver(func(context.Context, string) ([]net.IPAddr, error) {
				resolves++
				address := "8.8.8.8"
				if resolves > 1 {
					address = "127.0.0.1"
				}
				return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
			}), nil)
			checker := diagnostic.NewInjectedTracerouteChecker(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				commands++
				if args[len(args)-1] != "8.8.8.8" {
					return nil, errors.New("blocked destination reached command")
				}
				return []byte("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  8.8.8.8  1 ms\n"), nil
			}, nil, policy)
			var logs bytes.Buffer
			handler, err := NewServerWithConfig(diagnostic.NewRunnerWithSupervisor(mustAPICheckerSupervisor(t, 1), checker), slog.New(slog.NewTextHandler(&logs, nil)), "test", publicServerConfig())
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"timeout_ms":100,"topology_mode":%q,"targets":[{"kind":"traceroute","address":"route-canary.example","attempts":3}]}`, mode)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-secret")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusUnprocessableEntity || bodyAdmissionErrorCode(t, recorder) != "network_policy_blocked" || commands != 1 || resolves != 2 {
				t.Fatalf("policy result status=%d body=%s commands=%d resolves=%d", recorder.Code, recorder.Body, commands, resolves)
			}
			for _, canary := range []string{"route-canary.example", "8.8.8.8", "127.0.0.1", "test-secret", "attempts", "topology", "results"} {
				if strings.Contains(recorder.Body.String(), canary) || strings.Contains(logs.String(), canary) {
					t.Errorf("policy failure leaked %q", canary)
				}
			}

		})
	}
}
