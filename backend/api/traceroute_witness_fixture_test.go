package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type tracerouteWitness struct {
	name              string
	outputs           []string
	errors            []error
	wantCode          string
	wantCompactRoutes int
	wantTopology      bool
	wantRawFailedTopo bool
}

func tracerouteWitnesses() []tracerouteWitness {
	unreached := "traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  192.0.2.1  1.0 ms\n2  * * *\n"
	reached := "traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  192.0.2.1  1.0 ms\n2  8.8.8.8  5.0 ms\n"
	failedReached := "traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  1.1.1.1  2.0 ms\n2  8.8.8.8  6.0 ms\n"
	return []tracerouteWitness{
		{name: "timeout", outputs: []string{unreached, ""}, errors: []error{nil, context.DeadlineExceeded}, wantCode: "timeout", wantCompactRoutes: 1, wantTopology: true},
		{name: "command-failure", outputs: []string{unreached, ""}, errors: []error{nil, errors.New("exit status 1")}, wantCode: "traceroute_failed", wantCompactRoutes: 1, wantTopology: true},
		{name: "failed-reached-parse", outputs: []string{failedReached}, errors: []error{errors.New("exit status 1")}, wantCode: "traceroute_failed", wantCompactRoutes: 0, wantRawFailedTopo: true},
		{name: "mixed-completed-failed", outputs: []string{reached, failedReached}, errors: []error{nil, errors.New("exit status 1")}, wantCompactRoutes: 1, wantTopology: true, wantRawFailedTopo: true},
	}
}

func TestCommittedTracerouteWitnessFixturesMatchProducerBytes(t *testing.T) {
	for index, witness := range tracerouteWitnesses() {
		t.Run(witness.name, func(t *testing.T) {
			report := tracerouteWitnessReport(t, witness, byte(index+0x90))
			assertTracerouteWitnessTruth(t, report, witness)
			full, err := marshalFullResponse(report)
			if err != nil {
				t.Fatal(err)
			}
			compact, err := marshalCompactResponse(report)
			if err != nil {
				t.Fatal(err)
			}
			assertTracerouteWitnessWireTruth(t, full, compact, witness)
			for mode, generated := range map[string][]byte{"full": full, "compact": compact} {
				name := "traceroute-" + witness.name + "-" + mode + "-report.json"
				path := filepath.Join("..", "..", "testdata", name)
				if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
					if err := os.WriteFile(path, generated, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				committed, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", name, err)
				}
				if !bytes.Equal(committed, generated) {
					t.Fatalf("%s is stale: producer bytes differ", name)
				}
			}
		})
	}
}

func tracerouteWitnessReport(t *testing.T, witness tracerouteWitness, idByte byte) diagnostic.Report {
	t.Helper()
	call := 0
	checker := diagnostic.NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		output, err := witness.outputs[call], witness.errors[call]
		call++
		return []byte(output), err
	}, nil, nil)
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checker)
	target := diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: "8.8.8.8", Attempts: len(witness.outputs)}
	report, err := runner.RunWithID(context.Background(), fmt.Sprintf("%024x", idByte), diagnostic.Request{Targets: []diagnostic.Target{target}})
	if err != nil {
		t.Fatal(err)
	}
	if call != len(witness.outputs) {
		t.Fatalf("producer calls=%d, want %d", call, len(witness.outputs))
	}
	normalizeFixtureReport(&report, int64(20+idByte))
	return report
}

func assertTracerouteWitnessTruth(t *testing.T, report diagnostic.Report, witness tracerouteWitness) {
	t.Helper()
	if len(report.Results) != 1 {
		t.Fatalf("results=%d", len(report.Results))
	}
	result := report.Results[0]
	if result.ErrorCode != witness.wantCode {
		t.Fatalf("error_code=%q, want %q", result.ErrorCode, witness.wantCode)
	}
	_, hasTopology := result.Details["topology"]
	if hasTopology != witness.wantTopology {
		t.Fatalf("representative topology present=%t, want %t", hasTopology, witness.wantTopology)
	}
	attempts := result.Details["attempts"].([]diagnostic.TraceAttempt)
	rawFailedTopology := false
	for _, attempt := range attempts {
		if attempt.ErrorCode != "" && attempt.Topology != nil {
			rawFailedTopology = true
		}
	}
	if rawFailedTopology != witness.wantRawFailedTopo {
		t.Fatalf("raw failed topology present=%t, want %t: %+v", rawFailedTopology, witness.wantRawFailedTopo, attempts)
	}
	assertResultAllowedByFindingContract(t, result)
}

func assertTracerouteWitnessWireTruth(t *testing.T, full, compact []byte, witness tracerouteWitness) {
	t.Helper()
	var fullReport, compactReport diagnostic.Report
	if err := json.Unmarshal(full, &fullReport); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(compact, &compactReport); err != nil {
		t.Fatal(err)
	}
	assertResultAllowedByFindingContract(t, fullReport.Results[0])
	assertResultAllowedByFindingContract(t, compactReport.Results[0])
	if _, ok := compactReport.Results[0].Details["attempts"]; ok {
		t.Fatal("compact result retained raw attempts")
	}
	if _, ok := compactReport.Results[0].Details["topology"]; ok {
		t.Fatal("compact result retained representative topology")
	}
	if compactReport.CompactTopology == nil || len(compactReport.CompactTopology.Routes) != witness.wantCompactRoutes || compactReport.CompactTopology.Stats.Routes.Total != witness.wantCompactRoutes {
		t.Fatalf("compact route truth = %+v, want %d routes", compactReport.CompactTopology, witness.wantCompactRoutes)
	}
}

func assertResultAllowedByFindingContract(t *testing.T, result diagnostic.Result) {
	t.Helper()
	for _, shape := range diagnostic.ProducerFindingContract().ResultShapes {
		if shape.Status != result.Status || shape.ErrorCode != result.ErrorCode || !containsKind(shape.Kinds, result.Kind) {
			continue
		}
		allowed := make(map[string]bool, len(shape.RequiredDetails)+len(shape.OptionalDetails))
		compatible := true
		for _, detail := range shape.RequiredDetails {
			allowed[detail.Key] = true
			if _, ok := result.Details[detail.Key]; !ok {
				compatible = false
			}
		}
		for _, detail := range shape.OptionalDetails {
			allowed[detail.Key] = true
		}
		for key := range result.Details {
			if !allowed[key] {
				compatible = false
				break
			}
		}
		if compatible {
			return
		}
	}
	t.Fatalf("producer result has no semantic shape: kind=%s status=%s error_code=%q details=%v", result.Kind, result.Status, result.ErrorCode, result.Details)
}

func containsKind(kinds []diagnostic.Kind, want diagnostic.Kind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}
