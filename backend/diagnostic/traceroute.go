package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"

	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latencyJumpThresholdMS = 50.0

const (
	// MaxTraceHops is the largest hop number accepted from traceroute output.
	MaxTraceHops = 30
	// MaxTraceOutputBytes bounds combined traceroute stdout and stderr.
	MaxTraceOutputBytes = 256 << 10
	// MaxTraceLatencyMS bounds an observed RTT to the largest supported
	// per-attempt request timeout. Larger values cannot be genuine observations
	// from a command that is terminated at MaxTimeout.
	MaxTraceLatencyMS = float64(MaxTimeout / time.Millisecond)
)

// ErrTraceOutputLimit reports that traceroute produced more output than allowed.
var ErrTraceOutputLimit = errors.New("traceroute output exceeds limit")

// ErrTraceHopLimit reports a hop number beyond MaxTraceHops.
var ErrTraceHopLimit = errors.New("traceroute hop exceeds limit")

// ErrTraceHopSequence reports a non-positive, duplicate, or decreasing hop.
var ErrTraceHopSequence = errors.New("traceroute hops are not strictly increasing")

// ErrTraceLatency reports an invalid or non-finite latency token.
var ErrTraceLatency = errors.New("invalid traceroute latency")

type TopologyNode struct {
	ID          string       `json:"id"`
	Hop         int          `json:"hop"`
	Address     string       `json:"address,omitempty"`
	LatencyMS   float64      `json:"latency_ms,omitempty"`
	Status      string       `json:"status"`
	PublicIP    bool         `json:"public_ip,omitempty"`
	Geolocation *GeoLocation `json:"geolocation,omitempty"`
	ASN         *ASNInfo     `json:"asn,omitempty"`
}

type TopologyLink struct {
	From           string  `json:"from"`
	To             string  `json:"to"`
	Status         string  `json:"status"`
	LatencyDeltaMS float64 `json:"latency_delta_ms,omitempty"`
}

type Topology struct {
	Reached bool           `json:"reached"`
	Nodes   []TopologyNode `json:"nodes"`
	Links   []TopologyLink `json:"links"`
}

type TraceAttempt struct {
	Attempt   int       `json:"attempt"`
	Status    Status    `json:"status"`
	Topology  *Topology `json:"topology,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type traceCommand func(context.Context, string, ...string) ([]byte, error)

type TracerouteChecker struct {
	Command traceCommand
	GeoIP   GeoIPLookup
	Policy  *NetworkPolicy
}

func (TracerouteChecker) Kind() Kind { return KindTraceroute }

func (c TracerouteChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	destination, err := traceDestination(target.Address)
	if err != nil {
		result := baseResult(KindTraceroute, target.Address, started, err)
		result.ErrorCode = "invalid_address"
		return result
	}

	run := c.Command
	if run == nil {
		run = runTraceCommand
	}
	result := baseResult(KindTraceroute, target.Address, started, nil)
	attemptCount := target.Attempts
	if attemptCount == 0 {
		attemptCount = DefaultTraceAttempts
	}
	attempts := make([]TraceAttempt, 0, attemptCount)
	var representative *Topology
	reached := 0
	unreached := 0
	degraded := false
	executionFailed := 0
	timedOut := 0
	cancelled := 0
	for attemptNumber := 1; attemptNumber <= attemptCount; attemptNumber++ {
		attemptDuration := attemptTimeout(ctx)
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, attemptDuration)
		commandDestination := destination
		if c.Policy != nil {
			addresses, resolveErr := c.Policy.Resolve(attemptCtx, destination)
			if resolveErr != nil {
				cancelAttempt()
				return networkPolicyResult(KindTraceroute, target.Address, started, resolveErr)
			}
			commandDestination = addresses[0].String()
		}
		name, args := traceCommandSpec(attemptDuration, commandDestination)
		output, commandErr := run(attemptCtx, name, args...)
		if contextErr := attemptCtx.Err(); contextErr != nil {
			commandErr = contextErr
		}
		cancelAttempt()
		topology, parseErr := parseTraceroute(string(output), destination)
		if parseErr != nil {
			errorCode := "traceroute_failed"
			if commandErr != nil {
				errorCode = traceCommandErrorCode(commandErr)
				parseErr = fmt.Errorf("traceroute 실행 실패: %w", commandErr)
			}
			executionFailed++
			if errorCode == "timeout" {
				timedOut++
			} else if errorCode == "cancelled" {
				cancelled++
			}
			attempts = append(attempts, TraceAttempt{Attempt: attemptNumber, Status: StatusUnreachable, ErrorCode: errorCode, Message: parseErr.Error()})
			continue
		}

		attemptStatus := topologyStatus(topology)
		topologyCopy := topology
		attempt := TraceAttempt{Attempt: attemptNumber, Status: attemptStatus, Topology: &topologyCopy}
		if commandErr != nil {
			attempt.Status = StatusUnreachable
			attempt.ErrorCode = traceCommandErrorCode(commandErr)
			attempt.Message = commandErr.Error()
			executionFailed++
			if attempt.ErrorCode == "timeout" {
				timedOut++
			} else if attempt.ErrorCode == "cancelled" {
				cancelled++
			}
		}
		attempts = append(attempts, attempt)
		if representative == nil || (!representative.Reached && topology.Reached) {
			representative = &topologyCopy
		}
		if topology.Reached && commandErr == nil {
			reached++
		} else if commandErr == nil {
			unreached++
		}
		if attemptStatus == StatusDegraded {
			degraded = true
		}
	}
	geoIPProviderFailures := 0
	var geoIPEnrichment *EnrichmentCoverage
	if c.GeoIP != nil {
		coverage := enrichTopologiesWithCoverage(ctx, attempts, c.GeoIP)
		geoIPProviderFailures = enrichmentFailureCount(coverage.Failures)
		geoIPEnrichment = &coverage
	}

	result.Details = map[string]any{
		"attempts":                  attempts,
		"attempts_total":            attemptCount,
		"attempts_reached":          reached,
		"attempts_failed":           attemptCount - reached,
		"attempts_unreached":        unreached,
		"attempts_execution_failed": executionFailed,
		"attempts_timed_out":        timedOut,
		"attempts_cancelled":        cancelled,
	}
	if geoIPProviderFailures > 0 {
		result.Details["geoip_provider_failures"] = geoIPProviderFailures
	}
	if geoIPEnrichment != nil {
		result.Details["geoip_enrichment"] = *geoIPEnrichment
	}
	if representative != nil {
		result.Details["topology"] = *representative
	}
	switch {
	case reached == 0:
		result.Status = StatusUnreachable
	case reached < attemptCount || degraded:
		result.Status = StatusDegraded
	default:
		result.Status = StatusHealthy
	}
	switch result.Status {
	case StatusHealthy:
		result.Message = fmt.Sprintf("traceroute %d회 모두 대상에 정상 도달했습니다.", attemptCount)
	case StatusDegraded:
		result.Message = fmt.Sprintf("traceroute %d회 중 %d회 도달했습니다. 경로 분기와 품질을 확인하세요.", attemptCount, reached)
	default:
		if executionFailed > 0 {
			result.Message = fmt.Sprintf("traceroute %d회 실행이 완료되지 않았습니다.", attemptCount)
			result.ErrorCode = summarizeTraceExecutionErrors(executionFailed, timedOut, cancelled)
		} else if representative == nil {
			result.Message = fmt.Sprintf("traceroute %d회 모두 실행 결과를 해석하지 못했습니다.", attemptCount)
			result.ErrorCode = "traceroute_failed"
		} else {
			result.Message = fmt.Sprintf("traceroute %d회 모두 대상에 도달하지 못했습니다.", attemptCount)
			result.ErrorCode = "destination_unreached"
		}
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	return result
}

func traceCommandErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "traceroute_failed"
	}
}

func summarizeTraceExecutionErrors(total, timedOut, cancelled int) string {
	switch {
	case total <= 0:
		return ""
	case timedOut == total:
		return "timeout"
	case cancelled == total:
		return "cancelled"
	case total-timedOut-cancelled == total:
		return "traceroute_failed"
	default:
		return "traceroute_execution_incomplete"
	}
}

func enrichTopologies(ctx context.Context, attempts []TraceAttempt, lookup GeoIPLookup) int {
	return enrichmentFailureCount(enrichTopologiesWithCoverage(ctx, attempts, lookup).Failures)
}

func enrichTopologiesWithCoverage(ctx context.Context, attempts []TraceAttempt, lookup GeoIPLookup) EnrichmentCoverage {
	type enrichment struct {
		public   bool
		metadata IPMetadata
	}
	cache := make(map[string]enrichment)
	addresses := make(map[string]net.IP)
	for attemptIndex := range attempts {
		if attempts[attemptIndex].Topology == nil {
			continue
		}
		for nodeIndex := range attempts[attemptIndex].Topology.Nodes {
			node := &attempts[attemptIndex].Topology.Nodes[nodeIndex]
			ip := net.ParseIP(node.Address)
			if ip == nil {
				continue
			}
			known := enrichment{public: isPublicIP(ip)}
			cache[ip.String()] = known
			if known.public {
				addresses[ip.String()] = ip
			}
		}
	}
	jobs := make(chan net.IP)
	var mu sync.Mutex
	coverage := EnrichmentCoverage{Provider: "geoip", Source: EnrichmentSourceNone, Failures: []EnrichmentFailure{}}
	failureCounts := make(map[GeoIPErrorKind]int)
	var workers sync.WaitGroup
	workerCount := min(6, len(addresses))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ip := range jobs {
				metadata, err := lookup.Lookup(ctx, ip)
				if err != nil {
					mu.Lock()
					kind, _ := normalizedGeoIPFailure(err)
					failureCounts[kind]++
					mu.Unlock()
					continue
				}
				mu.Lock()
				if metadata.Source == GeoIPSourceCache {
					coverage.CacheHits++
				} else {
					coverage.UpstreamFetches++
				}
				if age := boundedGeoIPAgeMS(metadata, time.Now().UTC()); age > coverage.MaxAgeMS {
					coverage.MaxAgeMS = age
				}
				known := cache[ip.String()]
				known.metadata = metadata
				cache[ip.String()] = known
				mu.Unlock()
			}
		}()
	}
	for _, ip := range addresses {
		jobs <- ip
	}
	close(jobs)
	workers.Wait()

	for attemptIndex := range attempts {
		if attempts[attemptIndex].Topology == nil {
			continue
		}
		for nodeIndex := range attempts[attemptIndex].Topology.Nodes {
			node := &attempts[attemptIndex].Topology.Nodes[nodeIndex]
			ip := net.ParseIP(node.Address)
			if ip == nil {
				continue
			}
			known := cache[ip.String()]
			node.PublicIP = known.public
			node.Geolocation = known.metadata.Geolocation
			node.ASN = known.metadata.ASN
		}
	}
	coverage.Source = enrichmentSource(coverage.CacheHits, coverage.UpstreamFetches)
	for _, kind := range geoIPFailureKinds() {
		if count := failureCounts[kind]; count > 0 {
			coverage.Failures = append(coverage.Failures, EnrichmentFailure{Kind: kind, Count: count, Retryable: geoIPFailureRetryable(kind)})
		}
	}
	return coverage
}

func enrichmentSource(cacheHits, upstreamFetches int) EnrichmentSource {
	switch {
	case cacheHits > 0 && upstreamFetches > 0:
		return EnrichmentSourceMixed
	case cacheHits > 0:
		return EnrichmentSourceCache
	case upstreamFetches > 0:
		return EnrichmentSourceUpstream
	default:
		return EnrichmentSourceNone
	}
}

func boundedGeoIPAgeMS(metadata IPMetadata, now time.Time) int64 {
	if metadata.FetchedAt.IsZero() || now.Before(metadata.FetchedAt) {
		return 0
	}
	age := now.Sub(metadata.FetchedAt)
	limit := defaultGeoIPCacheTTL
	if !metadata.ExpiresAt.IsZero() && metadata.ExpiresAt.After(metadata.FetchedAt) {
		if ttl := metadata.ExpiresAt.Sub(metadata.FetchedAt); ttl < limit {
			limit = ttl
		}
	}
	if age > limit {
		age = limit
	}
	if age <= 0 {
		return 0
	}
	return age.Milliseconds()
}

func normalizedGeoIPFailure(err error) (GeoIPErrorKind, bool) {
	var typed *GeoIPError
	if errors.As(err, &typed) && typed != nil && validGeoIPErrorKind(typed.Kind) {
		return typed.Kind, geoIPFailureRetryable(typed.Kind)
	}
	return GeoIPErrorUnavailable, true
}

func geoIPFailureKinds() []GeoIPErrorKind {
	return []GeoIPErrorKind{GeoIPErrorBusy, GeoIPErrorCancelled, GeoIPErrorMalformed, GeoIPErrorNotFound, GeoIPErrorPolicy, GeoIPErrorRateLimited, GeoIPErrorTimeout, GeoIPErrorUnavailable}
}

func validGeoIPErrorKind(kind GeoIPErrorKind) bool {
	for _, allowed := range geoIPFailureKinds() {
		if kind == allowed {
			return true
		}
	}
	return false
}

func geoIPFailureRetryable(kind GeoIPErrorKind) bool {
	switch kind {
	case GeoIPErrorRateLimited, GeoIPErrorTimeout, GeoIPErrorUnavailable, GeoIPErrorBusy:
		return true
	default:
		return false
	}
}

func enrichmentFailureCount(failures []EnrichmentFailure) int {
	total := 0
	for _, failure := range failures {
		total += failure.Count
	}
	return total
}

func traceDestination(address string) (string, error) {
	address = strings.TrimSpace(strings.TrimSuffix(address, "."))
	if net.ParseIP(address) != nil {
		return address, nil
	}
	if len(address) == 0 || len(address) > 253 {
		return "", fmt.Errorf("traceroute address must be an IP address or hostname")
	}
	for _, label := range strings.Split(address, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("traceroute address must be an IP address or hostname")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("traceroute address must be an IP address or hostname")
			}
		}
	}
	return address, nil
}

var latencyPattern = regexp.MustCompile(`(\S+)\s*ms(?:\s|$)`)
var headerAddressPattern = regexp.MustCompile(`\(([^()]+)\)`)
var windowsHeaderAddressPattern = regexp.MustCompile(`\[([^\[\]]+)\]`)

func parseTraceroute(output, requestedDestination string) (Topology, error) {
	if len(output) > MaxTraceOutputBytes {
		return Topology{}, ErrTraceOutputLimit
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return Topology{}, fmt.Errorf("traceroute output did not contain any hops")
	}
	destination := requestedDestination
	headerIndex := 0
	headerFound := false
	windowsOutput := false
	for i, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if !strings.Contains(lower, "traceroute to ") && !strings.HasPrefix(lower, "tracing route to ") {
			continue
		}
		headerIndex = i
		headerFound = true
		windowsOutput = strings.HasPrefix(lower, "tracing route to ")
		if windowsOutput {
			if match := windowsHeaderAddressPattern.FindStringSubmatch(line); len(match) == 2 {
				destination = match[1]
			} else {
				trimmed := strings.TrimSpace(line)
				destination = strings.TrimSpace(trimmed[len("Tracing route to "):])
			}
		} else if match := headerAddressPattern.FindStringSubmatch(line); len(match) == 2 {
			destination = match[1]
		}
		break
	}
	if !headerFound {
		return Topology{}, fmt.Errorf("traceroute output did not contain a header")
	}
	topology := Topology{Nodes: []TopologyNode{{ID: "hop-0", Hop: 0, Address: "local", Status: "healthy"}}}
	previousHop := 0
	for _, line := range lines[headerIndex+1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hop, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if hop > MaxTraceHops {
			return Topology{}, ErrTraceHopLimit
		}
		if hop <= previousHop {
			return Topology{}, ErrTraceHopSequence
		}
		previousHop = hop
		node := TopologyNode{ID: fmt.Sprintf("hop-%d", hop), Hop: hop, Status: "unknown"}
		if windowsOutput {
			parsed, parseErr := parseWindowsTraceHop(fields, node)
			if parseErr != nil {
				return Topology{}, parseErr
			}
			node = parsed
		} else if fields[1] != "*" {
			node.Address = strings.Trim(fields[1], "()")
			if net.ParseIP(node.Address) == nil && len(fields) > 2 {
				candidate := strings.Trim(fields[2], "()")
				if net.ParseIP(candidate) != nil {
					node.Address = candidate
				}
			}
			if match := latencyPattern.FindStringSubmatch(line); len(match) == 2 {
				latencyText := strings.TrimPrefix(match[1], "<")
				latency, parseErr := strconv.ParseFloat(latencyText, 64)
				if parseErr != nil || !isValidTraceLatency(latency) {
					return Topology{}, fmt.Errorf("%w: %q", ErrTraceLatency, match[1])
				}
				node.LatencyMS = latency
				node.Status = "healthy"
			}
		}
		topology.Nodes = append(topology.Nodes, node)
		if node.Address == destination || node.Address == requestedDestination {
			topology.Reached = true
			break
		}
	}
	if len(topology.Nodes) == 1 {
		return Topology{}, fmt.Errorf("traceroute output did not contain any hops")
	}
	if !topology.Reached {
		nextHop := topology.Nodes[len(topology.Nodes)-1].Hop + 1
		topology.Nodes = append(topology.Nodes, TopologyNode{ID: fmt.Sprintf("hop-%d", nextHop), Hop: nextHop, Address: requestedDestination, Status: "failure"})
	}
	classifyTopology(&topology)
	return topology, nil
}

// parseWindowsTraceHop selects the minimum finite latency from tracert's three
// probes. This is deterministic and avoids a timeout probe biasing the value.
func parseWindowsTraceHop(fields []string, node TopologyNode) (TopologyNode, error) {
	position := 1
	minimum := 0.0
	hasLatency := false
	for probe := 0; probe < 3; probe++ {
		if position >= len(fields) {
			return TopologyNode{}, fmt.Errorf("%w: incomplete Windows probe row", ErrTraceLatency)
		}
		if fields[position] == "*" {
			position++
			continue
		}
		latencyText := strings.TrimPrefix(fields[position], "<")
		position++
		if position >= len(fields) || !strings.EqualFold(fields[position], "ms") {
			return TopologyNode{}, fmt.Errorf("%w: malformed Windows probe", ErrTraceLatency)
		}
		position++
		latency, err := strconv.ParseFloat(latencyText, 64)
		if err != nil || !isValidTraceLatency(latency) {
			return TopologyNode{}, fmt.Errorf("%w: %q", ErrTraceLatency, latencyText)
		}
		if !hasLatency || latency < minimum {
			minimum = latency
			hasLatency = true
		}
	}
	for _, candidate := range fields[position:] {
		candidate = strings.Trim(candidate, "[]()")
		if net.ParseIP(candidate) != nil {
			node.Address = candidate
			break
		}
	}
	if hasLatency {
		node.LatencyMS = minimum
		node.Status = "healthy"
	}
	return node, nil
}

func classifyTopology(topology *Topology) {
	for i := 1; i < len(topology.Nodes); i++ {
		previous, current := topology.Nodes[i-1], &topology.Nodes[i]
		link := TopologyLink{From: previous.ID, To: current.ID, Status: current.Status}
		if current.Status == "healthy" && previous.Status == "healthy" && previous.Hop > 0 {
			latencyDelta, valid := roundedTraceLatencyDelta(current.LatencyMS, previous.LatencyMS)
			if valid {
				link.LatencyDeltaMS = latencyDelta
			}
			if valid && link.LatencyDeltaMS >= latencyJumpThresholdMS {
				link.Status = "degraded"
				current.Status = "degraded"
			}
		}
		topology.Links = append(topology.Links, link)
	}
}

func isValidTraceLatency(latency float64) bool {
	return latency >= 0 && latency <= MaxTraceLatencyMS && !math.IsNaN(latency) && !math.IsInf(latency, 0)
}

func roundedTraceLatencyDelta(current, previous float64) (float64, bool) {
	if math.IsNaN(current) || math.IsInf(current, 0) || math.IsNaN(previous) || math.IsInf(previous, 0) {
		return 0, false
	}
	delta := current - previous
	if math.IsNaN(delta) || math.IsInf(delta, 0) {
		return 0, false
	}
	scaled := delta * 100
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
		return 0, false
	}
	return math.Round(scaled) / 100, true
}

func topologyStatus(topology Topology) Status {
	if !topology.Reached {
		return StatusUnreachable
	}
	for _, node := range topology.Nodes {
		if node.Status == "unknown" || node.Status == "degraded" {
			return StatusDegraded
		}
	}
	return StatusHealthy
}
