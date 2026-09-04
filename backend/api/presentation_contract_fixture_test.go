package api

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type presentationChecker struct {
	kind   diagnostic.Kind
	result func(diagnostic.Target) diagnostic.Result
}

func (checker presentationChecker) Kind() diagnostic.Kind { return checker.kind }

func (checker presentationChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	result := checker.result(target)
	result.Kind = target.Kind
	result.Address = target.Address
	result.StartedAt = fixtureTime
	return result
}

type cancellationPresentationChecker struct {
	started  chan struct{}
	returned chan struct{}
}

func (checker cancellationPresentationChecker) Kind() diagnostic.Kind {
	return diagnostic.KindDNS
}

func (checker cancellationPresentationChecker) Check(ctx context.Context, _ diagnostic.Target) diagnostic.Result {
	close(checker.started)
	<-ctx.Done()
	close(checker.returned)
	var result diagnostic.Result
	return result
}

func presentationFixtureBytes(t *testing.T) []byte {
	t.Helper()
	sources := presentationScenarioSources(t)
	contract, err := diagnostic.BuildPresentationContract(sources, presentationCoverageProbes(), fixtureTime)
	if err != nil {
		t.Fatalf("build presentation contract: %v", err)
	}
	encoded, err := diagnostic.MarshalPresentationContract(contract)
	if err != nil {
		t.Fatalf("marshal presentation contract: %v", err)
	}
	return encoded
}

func presentationScenarioSources(t *testing.T) []diagnostic.PresentationScenarioSource {
	t.Helper()
	future := fixtureTime.Add(90 * 24 * time.Hour)
	expiredDetails := map[string]any{
		diagnostic.ResultDetailCertificateBefore: fixtureTime.Add(-24 * time.Hour).Format(time.RFC3339),
		diagnostic.ResultDetailCertificateAfter:  fixtureTime.Format(time.RFC3339),
	}
	notYetValidDetails := map[string]any{
		diagnostic.ResultDetailCertificateBefore: fixtureTime.Add(24 * time.Hour).Format(time.RFC3339),
		diagnostic.ResultDetailCertificateAfter:  fixtureTime.Add(48 * time.Hour).Format(time.RFC3339),
	}
	degradedTopology := diagnostic.Topology{
		Reached: true,
		Nodes: []diagnostic.TopologyNode{
			{ID: "hop-1", Hop: 1, Address: "192.0.2.10", Status: "healthy"},
			{ID: "hop-2", Hop: 2, Address: "198.51.100.20", Status: "degraded"},
		},
		Links: []diagnostic.TopologyLink{{From: "hop-1", To: "hop-2", Status: "degraded", LatencyDeltaMS: 70}},
	}
	stablePathA := diagnostic.Topology{Reached: true, Nodes: []diagnostic.TopologyNode{{ID: "a", Hop: 1, Address: "192.0.2.1", Status: "healthy"}, {ID: "end", Hop: 2, Address: "203.0.113.10", Status: "healthy"}}, Links: []diagnostic.TopologyLink{}}
	stablePathB := diagnostic.Topology{Reached: true, Nodes: []diagnostic.TopologyNode{{ID: "b", Hop: 1, Address: "192.0.2.2", Status: "degraded"}, {ID: "end", Hop: 2, Address: "203.0.113.10", Status: "healthy"}}, Links: []diagnostic.TopologyLink{{From: "b", To: "end", Status: "degraded"}}}

	type recipe struct {
		name    string
		code    diagnostic.FindingCode
		targets []diagnostic.Target
		results map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result
	}
	single := func(kind diagnostic.Kind, address string, result diagnostic.Result) ([]diagnostic.Target, map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result) {
		return []diagnostic.Target{{Kind: kind, Address: address}}, map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result{
			kind: func(diagnostic.Target) diagnostic.Result { return result },
		}
	}
	trace := func(status diagnostic.Status, errorCode string, details map[string]any) diagnostic.Result {
		return diagnostic.Result{Status: status, ErrorCode: errorCode, Details: details}
	}
	var recipes []recipe
	addSingle := func(name string, code diagnostic.FindingCode, kind diagnostic.Kind, address string, result diagnostic.Result) {
		targets, results := single(kind, address, result)
		recipes = append(recipes, recipe{name: name, code: code, targets: targets, results: results})
	}

	addSingle("finding.checker_capacity_unavailable", diagnostic.FindingCheckerCapacityUnavailable, diagnostic.KindDNS, "capacity.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "checker_capacity_unavailable", Details: map[string]any{}})
	addSingle("finding.checker_panic", diagnostic.FindingCheckerPanic, diagnostic.KindDNS, "panic.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "checker_panic", Details: map[string]any{}})
	addSingle("finding.dns_resolution_failed", diagnostic.FindingDNSResolutionFailed, diagnostic.KindDNS, "missing.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "connection_failed", Details: map[string]any{}})
	addSingle("finding.endpoint_connect_failed", diagnostic.FindingEndpointConnectFailed, diagnostic.KindTCP, "203.0.113.10:443", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "connection_failed", Details: map[string]any{}})
	addSingle("finding.execution_timeout", diagnostic.FindingExecutionTimeout, diagnostic.KindTraceroute, "timeout.example.test", trace(diagnostic.StatusUnreachable, "timeout", traceCounters(1, 0, 0, 1, 1, 0)))
	addSingle("finding.http_unexpected_status", diagnostic.FindingHTTPUnexpectedStatus, diagnostic.KindHTTP, "http://status.example.test/health", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "unexpected_status", Details: map[string]any{"status_code": 503, "expected_status": 200}})
	addSingle("finding.invalid_target", diagnostic.FindingInvalidTarget, diagnostic.KindHTTP, "http://invalid.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "invalid_url", Details: map[string]any{}})
	addSingle("finding.service_greeting_unverified", diagnostic.FindingServiceGreetingUnverified, diagnostic.KindSMTP, "mail.example.test:25", diagnostic.Result{Status: diagnostic.StatusDegraded, ErrorCode: diagnostic.ResultErrorServiceGreetingUnverified, Details: map[string]any{}})
	addSingle("finding.target_policy_blocked", diagnostic.FindingTargetPolicyBlocked, diagnostic.KindHTTP, "http://policy.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "network_policy_blocked", Details: map[string]any{}})
	addSingle("finding.tls_certificate_expired", diagnostic.FindingTLSCertificateExpired, diagnostic.KindHTTPS, "https://expired.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: diagnostic.ResultErrorTLSCertificateExpired, Details: expiredDetails})
	addSingle("finding.tls_certificate_expiring", diagnostic.FindingTLSCertificateExpiring, diagnostic.KindHTTPS, "https://expiring.example.test", diagnostic.Result{Status: diagnostic.StatusHealthy, Details: map[string]any{"status_code": 200, "expected_status": 200, diagnostic.ResultDetailTLSVersion: "TLS 1.3", diagnostic.ResultDetailCertificateExpires: fixtureTime.Add(7 * 24 * time.Hour)}})
	addSingle("finding.tls_certificate_not_yet_valid", diagnostic.FindingTLSCertificateNotYetValid, diagnostic.KindHTTPS, "https://future.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: diagnostic.ResultErrorTLSCertificateNotYetValid, Details: notYetValidDetails})
	addSingle("finding.tls_downgrade", diagnostic.FindingTLSDowngrade, diagnostic.KindHTTPS, "https://downgrade.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "tls_downgrade", Details: map[string]any{}})
	addSingle("finding.tls_handshake_failed", diagnostic.FindingTLSHandshakeFailed, diagnostic.KindHTTPS, "https://handshake.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: diagnostic.ResultErrorTLSHandshakeFailed})
	addSingle("finding.tls_hostname_mismatch", diagnostic.FindingTLSHostnameMismatch, diagnostic.KindHTTPS, "https://hostname.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: diagnostic.ResultErrorTLSHostnameMismatch})
	addSingle("finding.tls_untrusted", diagnostic.FindingTLSUntrusted, diagnostic.KindHTTPS, "https://untrusted.example.test", diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: diagnostic.ResultErrorTLSUntrusted})

	recipes = append(recipes, recipe{
		name: "finding.traceroute_execution_failed", code: diagnostic.FindingTracerouteExecutionFailed,
		targets: []diagnostic.Target{
			{Kind: diagnostic.KindTraceroute, Address: "execution.example.test", Attempts: 1},
			{Kind: diagnostic.KindTraceroute, Address: "execution.example.test", Attempts: 2},
		},
		results: map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result{
			diagnostic.KindTraceroute: func(target diagnostic.Target) diagnostic.Result {
				if target.Attempts == 1 {
					return trace(diagnostic.StatusUnreachable, "traceroute_failed", traceCounters(1, 0, 0, 1, 0, 0))
				}
				return trace(diagnostic.StatusUnreachable, "traceroute_execution_incomplete", traceCounters(2, 0, 0, 2, 1, 1))
			},
		},
	})
	addSingle("finding.traceroute_partial_reachability", diagnostic.FindingTraceroutePartialReachability, diagnostic.KindTraceroute, "partial.example.test", trace(diagnostic.StatusDegraded, "", traceCounters(2, 1, 1, 0, 0, 0)))
	degradedDetails := traceCounters(1, 1, 0, 0, 0, 0)
	degradedDetails["topology"] = degradedTopology
	addSingle("finding.traceroute_path_degraded", diagnostic.FindingTraceroutePathDegraded, diagnostic.KindTraceroute, "degraded.example.test", trace(diagnostic.StatusDegraded, "", degradedDetails))
	unstableDetails := traceCounters(2, 2, 0, 0, 0, 0)
	unstableDetails["attempts"] = []diagnostic.TraceAttempt{
		{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: &stablePathA},
		{Attempt: 2, Status: diagnostic.StatusDegraded, Topology: &stablePathB},
	}
	addSingle("finding.traceroute_path_unstable", diagnostic.FindingTraceroutePathUnstable, diagnostic.KindTraceroute, "unstable.example.test", trace(diagnostic.StatusDegraded, "", unstableDetails))
	// Exercise the production nil-capability checker; do not replace this with
	// an injected result recipe that could drift from startup behavior.
	nilCapabilityTarget := diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: "unavailable.example.test", Attempts: 1}
	nilCapabilitySource := diagnostic.PresentationScenarioSource{
		Name: "finding.traceroute_unavailable", PrimaryFinding: diagnostic.FindingTracerouteUnavailable,
		Report: runNilCapabilityPresentationReport(t, fmt.Sprintf("%024x", 0x180), nilCapabilityTarget),
	}
	cancelledTarget := diagnostic.Target{Kind: diagnostic.KindDNS, Address: "cancelled.example.test"}
	cancelledSource := diagnostic.PresentationScenarioSource{
		Name: "finding.execution_cancelled", PrimaryFinding: diagnostic.FindingExecutionCancelled,
		Report: runParentCancellationPresentationReport(t, fmt.Sprintf("%024x", 0x181), cancelledTarget),
	}
	addSingle("finding.traceroute_unreachable", diagnostic.FindingTracerouteUnreachable, diagnostic.KindTraceroute, "unreachable.example.test", trace(diagnostic.StatusUnreachable, "destination_unreached", traceCounters(1, 0, 1, 0, 0, 0)))

	sources := make([]diagnostic.PresentationScenarioSource, 0, len(recipes)+10)
	for index, recipe := range recipes {
		report := runPresentationReport(t, fmt.Sprintf("%024x", 0x100+index), recipe.targets, recipe.results)
		sources = append(sources, diagnostic.PresentationScenarioSource{Name: recipe.name, PrimaryFinding: recipe.code, Report: report})
	}
	sources = append(sources, nilCapabilitySource)
	sources = append(sources, cancelledSource)

	healthyDNS := diagnostic.Result{Status: diagnostic.StatusHealthy, Details: map[string]any{"addresses": []string{"192.0.2.53"}, "answer_count": 1}}
	sources = append(sources, diagnostic.PresentationScenarioSource{
		Name: "control.dns_only_healthy",
		Report: runPresentationReport(t, fmt.Sprintf("%024x", 0x200), []diagnostic.Target{{Kind: diagnostic.KindDNS, Address: "healthy.example.test"}}, map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result{
			diagnostic.KindDNS: func(diagnostic.Target) diagnostic.Result { return healthyDNS },
		}),
	})

	serviceKinds := []diagnostic.Kind{diagnostic.KindIMAP, diagnostic.KindIMAPS, diagnostic.KindPOP3, diagnostic.KindPOP3S, diagnostic.KindSMTP, diagnostic.KindSMTPS, diagnostic.KindSSH, diagnostic.KindSubmission}
	for index, kind := range serviceKinds {
		details := map[string]any{diagnostic.ResultDetailVerificationScope: diagnostic.VerificationScopeServerGreeting}
		if kind == diagnostic.KindIMAPS || kind == diagnostic.KindPOP3S || kind == diagnostic.KindSMTPS {
			details[diagnostic.ResultDetailTLSVersion] = "TLS 1.3"
			details[diagnostic.ResultDetailCertificateExpires] = future
		}
		result := diagnostic.Result{Status: diagnostic.StatusHealthy, Details: details}
		sources = append(sources, diagnostic.PresentationScenarioSource{
			Name: "control.greeting_healthy." + string(kind),
			Report: runPresentationReport(t, fmt.Sprintf("%024x", 0x210+index), []diagnostic.Target{{Kind: kind, Address: string(kind) + ".example.test"}}, map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result{
				kind: func(diagnostic.Target) diagnostic.Result { return result },
			}),
		})
	}

	mixedTargets := []diagnostic.Target{
		{Kind: diagnostic.KindSMTP, Address: "mixed-mail.example.test:25"},
		{Kind: diagnostic.KindDNS, Address: "mixed-dns.example.test"},
		{Kind: diagnostic.KindTraceroute, Address: "mixed-trace.example.test", Attempts: 1},
	}
	mixedResults := map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result{
		diagnostic.KindSMTP: func(diagnostic.Target) diagnostic.Result {
			return diagnostic.Result{Status: diagnostic.StatusHealthy, Details: map[string]any{diagnostic.ResultDetailVerificationScope: diagnostic.VerificationScopeServerGreeting}}
		},
		diagnostic.KindDNS: func(diagnostic.Target) diagnostic.Result {
			return diagnostic.Result{Status: diagnostic.StatusUnreachable, ErrorCode: "connection_failed", Details: map[string]any{}}
		},
	}
	mixedCheckers := []diagnostic.Checker{
		presentationChecker{kind: diagnostic.KindDNS, result: mixedResults[diagnostic.KindDNS]},
		presentationChecker{kind: diagnostic.KindSMTP, result: mixedResults[diagnostic.KindSMTP]},
		diagnostic.NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
			return nil, context.Canceled
		}, nil, nil),
	}
	sources = append(sources, diagnostic.PresentationScenarioSource{
		Name:   "control.mixed_greeting_healthy_dns_failed",
		Report: runPresentationReportWithCheckers(t, fmt.Sprintf("%024x", 0x220), mixedTargets, mixedCheckers),
	})
	return sources
}

func runParentCancellationPresentationReport(t *testing.T, reportID string, target diagnostic.Target) diagnostic.Report {
	t.Helper()
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	checker := cancellationPresentationChecker{started: make(chan struct{}), returned: make(chan struct{})}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checker)
	ctx, cancel := context.WithCancel(context.Background())
	reportResult := make(chan struct {
		report diagnostic.Report
		err    error
	}, 1)
	go func() {
		report, runErr := runner.RunWithID(ctx, reportID, diagnostic.Request{Targets: []diagnostic.Target{target}})
		reportResult <- struct {
			report diagnostic.Report
			err    error
		}{report: report, err: runErr}
	}()
	<-checker.started
	cancel()
	got := <-reportResult
	if got.err != nil {
		t.Fatalf("run parent-cancelled presentation report: %v", got.err)
	}
	<-checker.returned
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), time.Second)
	defer stopShutdown()
	if snapshot := supervisor.ShutdownSnapshot(shutdownCtx); snapshot.Active != 0 || snapshot.Stuck != 0 {
		t.Fatalf("parent-cancelled presentation checker did not drain: %+v", snapshot)
	}
	normalizeFixtureReport(&got.report, 1)
	return got.report
}

func runNilCapabilityPresentationReport(t *testing.T, reportID string, target diagnostic.Target) diagnostic.Report {
	t.Helper()
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, diagnostic.NewTracerouteChecker(nil, nil, nil))
	report, err := runner.RunWithID(context.Background(), reportID, diagnostic.Request{Targets: []diagnostic.Target{target}})
	if err != nil {
		t.Fatalf("run nil-capability presentation report: %v", err)
	}
	normalizeFixtureReport(&report, 1)
	return report
}

func runPresentationReport(t *testing.T, reportID string, targets []diagnostic.Target, results map[diagnostic.Kind]func(diagnostic.Target) diagnostic.Result) diagnostic.Report {
	t.Helper()
	kinds := make([]diagnostic.Kind, 0, len(results))
	for kind := range results {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(left, right int) bool { return kinds[left] < kinds[right] })
	checkers := make([]diagnostic.Checker, 0, len(kinds))
	for _, kind := range kinds {
		checkers = append(checkers, presentationChecker{kind: kind, result: results[kind]})
	}
	return runPresentationReportWithCheckers(t, reportID, targets, checkers)
}

func runPresentationReportWithCheckers(t *testing.T, reportID string, targets []diagnostic.Target, checkers []diagnostic.Checker) diagnostic.Report {
	t.Helper()
	supervisor, err := diagnostic.NewCheckerSupervisor(len(targets))
	if err != nil {
		t.Fatal(err)
	}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checkers...)
	report, err := runner.RunWithID(context.Background(), reportID, diagnostic.Request{Targets: targets})
	if err != nil {
		t.Fatalf("run presentation report %s: %v", reportID, err)
	}
	normalizeFixtureReport(&report, int64(len(targets)))
	return report
}

func traceCounters(total, reached, unreached, executionFailed, timedOut, cancelled int) map[string]any {
	return map[string]any{
		"attempts_total": total, "attempts_reached": reached,
		"attempts_failed": unreached + executionFailed, "attempts_unreached": unreached,
		"attempts_execution_failed": executionFailed, "attempts_timed_out": timedOut,
		"attempts_cancelled": cancelled,
	}
}

func presentationCoverageProbes() []diagnostic.Result {
	validTrace := func(status diagnostic.Status) map[string]any {
		reached := 1
		if status == diagnostic.StatusUnreachable {
			reached = 0
		}
		return traceCounters(1, reached, 1-reached, 0, 0, 0)
	}
	return []diagnostic.Result{
		{Kind: diagnostic.KindDNS, Status: diagnostic.Status("future"), Details: map[string]any{}},
		{Kind: diagnostic.KindDNS, Status: diagnostic.StatusUnreachable, ErrorCode: "future_error", Details: map[string]any{}},
		{Kind: diagnostic.Kind("future"), Status: diagnostic.StatusUnreachable, ErrorCode: "connection_failed", Details: map[string]any{}},
		{Kind: diagnostic.KindDNS, Status: diagnostic.StatusUnreachable, ErrorCode: "connection_failed"},
		{Kind: diagnostic.KindHTTPS, Status: diagnostic.StatusUnreachable, ErrorCode: diagnostic.ResultErrorTLSCertificateExpired, Details: map[string]any{}},
		{Kind: diagnostic.KindSSH, Status: diagnostic.StatusHealthy, Details: map[string]any{}},
		{Kind: diagnostic.KindSSH, Status: diagnostic.StatusHealthy, Details: map[string]any{diagnostic.ResultDetailVerificationScope: diagnostic.VerificationScopeServerGreeting}},
		{Kind: diagnostic.KindHTTP, Status: diagnostic.StatusUnreachable, ErrorCode: "response_read_failed", Details: map[string]any{}},
		{Kind: diagnostic.KindDNS, Status: diagnostic.StatusHealthy, Details: map[string]any{}},
		{Kind: diagnostic.KindTCP, Status: diagnostic.StatusHealthy, Details: map[string]any{}},
		{Kind: diagnostic.KindHTTP, Status: diagnostic.StatusHealthy, Details: map[string]any{}},
		{Kind: diagnostic.KindHTTPS, Status: diagnostic.StatusHealthy, Details: map[string]any{"status_code": 200, "expected_status": 200}},
		{Kind: diagnostic.KindHTTPS, Status: diagnostic.StatusHealthy, Details: map[string]any{"status_code": 200, "expected_status": 200, diagnostic.ResultDetailTLSVersion: "TLS 1.3", diagnostic.ResultDetailCertificateExpires: 42}},
		{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy, Details: mergeDetails(validTrace(diagnostic.StatusHealthy), map[string]any{"geoip_provider_failures": 1})},
		{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy, Details: mergeDetails(validTrace(diagnostic.StatusHealthy), map[string]any{"geoip_enrichment": map[string]any{}})},
		{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusDegraded, Details: mergeDetails(traceCounters(1, 1, 0, 0, 0, 0), map[string]any{"attempts": []any{"malformed"}})},
		{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusUnreachable, ErrorCode: "destination_unreached", Details: traceCounters(1, 0, 0, 1, 0, 0)},
		{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy, Details: validTrace(diagnostic.StatusHealthy)},
		{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy, Details: map[string]any{}},
	}
}

func mergeDetails(left, right map[string]any) map[string]any {
	result := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

func assertPresentationScenarioStatuses(t *testing.T, contract diagnostic.PresentationContract) {
	t.Helper()
	byName := make(map[string]diagnostic.PresentationScenario, len(contract.Scenarios))
	for _, scenario := range contract.Scenarios {
		byName[scenario.Name] = scenario
	}
	dns := byName["control.dns_only_healthy"]
	if dns.Report.Status != diagnostic.StatusHealthy || dns.Report.Analysis == nil || dns.Report.Analysis.Verdict != diagnostic.VerdictHealthy {
		t.Fatalf("DNS-only control status/verdict = %s/%v", dns.Report.Status, dns.Report.Analysis)
	}
	for _, kind := range []diagnostic.Kind{diagnostic.KindIMAP, diagnostic.KindIMAPS, diagnostic.KindPOP3, diagnostic.KindPOP3S, diagnostic.KindSMTP, diagnostic.KindSMTPS, diagnostic.KindSSH, diagnostic.KindSubmission} {
		scenario := byName["control.greeting_healthy."+string(kind)]
		if scenario.Report.Status != diagnostic.StatusHealthy || scenario.Report.Analysis == nil || scenario.Report.Analysis.Verdict != diagnostic.VerdictInconclusive || len(scenario.Report.Results) != 1 || scenario.Report.Results[0].Kind != kind || scenario.Report.Results[0].Status != diagnostic.StatusHealthy {
			t.Fatalf("greeting control %q status/result/verdict drifted: %+v", kind, scenario.Report)
		}
	}
	mixed := byName["control.mixed_greeting_healthy_dns_failed"]
	if mixed.Report.Status != diagnostic.StatusDegraded || mixed.Report.Analysis == nil || mixed.Report.Analysis.Verdict != diagnostic.VerdictAttention || len(mixed.Report.Results) != 3 || !reflect.DeepEqual([]diagnostic.Status{mixed.Report.Results[0].Status, mixed.Report.Results[1].Status, mixed.Report.Results[2].Status}, []diagnostic.Status{diagnostic.StatusHealthy, diagnostic.StatusUnreachable, diagnostic.StatusUnreachable}) {
		t.Fatalf("mixed greeting/DNS/traceroute control status/result/verdict drifted: %+v", mixed.Report)
	}
	traceCancellation := mixed.Report.Results[2]
	if traceCancellation.Kind != diagnostic.KindTraceroute || traceCancellation.ErrorCode != "cancelled" || traceCancellation.Details == nil {
		t.Fatalf("production traceroute cancellation coverage witness drifted: %+v", traceCancellation)
	}
	cancelled := byName["finding.execution_cancelled"]
	if len(cancelled.Report.Results) != 1 || cancelled.Report.Results[0].Kind != diagnostic.KindDNS || cancelled.Report.Results[0].ErrorCode != "cancelled" || cancelled.Report.Results[0].Details != nil {
		t.Fatalf("parent cancellation must be the runner-owned generic no-details result: %+v", cancelled.Report)
	}
}
