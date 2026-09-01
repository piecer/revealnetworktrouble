package diagnostic

import (
	"context"
	"fmt"
	"math"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latencyJumpThresholdMS = 50.0

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
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	result := baseResult(KindTraceroute, target.Address, started, nil)
	attemptCount := target.Attempts
	if attemptCount == 0 {
		attemptCount = DefaultTraceAttempts
	}
	attempts := make([]TraceAttempt, 0, attemptCount)
	var representative *Topology
	reached := 0
	degraded := false
	for attemptNumber := 1; attemptNumber <= attemptCount; attemptNumber++ {
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, attemptTimeout(ctx))
		commandDestination := destination
		if c.Policy != nil {
			addresses, resolveErr := c.Policy.Resolve(attemptCtx, destination)
			if resolveErr != nil {
				cancelAttempt()
				return networkPolicyResult(KindTraceroute, target.Address, started, resolveErr)
			}
			commandDestination = addresses[0].String()
		}
		output, commandErr := run(attemptCtx, "traceroute", "-n", "-q", "1", "-w", "2", "-m", "30", commandDestination)
		cancelAttempt()
		topology, parseErr := parseTraceroute(string(output), destination)
		if parseErr != nil {
			if commandErr != nil {
				parseErr = fmt.Errorf("traceroute 실행 실패: %w", commandErr)
			}
			attempts = append(attempts, TraceAttempt{Attempt: attemptNumber, Status: StatusUnreachable, ErrorCode: "traceroute_failed", Message: parseErr.Error()})
			continue
		}

		attemptStatus := topologyStatus(topology)
		topologyCopy := topology
		attempts = append(attempts, TraceAttempt{Attempt: attemptNumber, Status: attemptStatus, Topology: &topologyCopy})
		if representative == nil || (!representative.Reached && topology.Reached) {
			representative = &topologyCopy
		}
		if topology.Reached {
			reached++
		}
		if attemptStatus == StatusDegraded {
			degraded = true
		}
	}
	if c.GeoIP != nil {
		enrichTopologies(ctx, attempts, c.GeoIP)
	}

	result.Details = map[string]any{
		"attempts":         attempts,
		"attempts_total":   attemptCount,
		"attempts_reached": reached,
		"attempts_failed":  attemptCount - reached,
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
		if representative == nil {
			result.Message = fmt.Sprintf("traceroute %d회 모두 실행 결과를 해석하지 못했습니다.", attemptCount)
			result.ErrorCode = "traceroute_failed"
		} else {
			result.Message = fmt.Sprintf("traceroute %d회 모두 대상에 도달하지 못했습니다.", attemptCount)
			result.ErrorCode = "destination_unreached"
		}
	}
	return result
}

func enrichTopologies(ctx context.Context, attempts []TraceAttempt, lookup GeoIPLookup) {
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
	var workers sync.WaitGroup
	workerCount := min(6, len(addresses))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ip := range jobs {
				metadata, err := lookup.Lookup(ctx, ip)
				if err != nil {
					continue
				}
				mu.Lock()
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

var latencyPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*ms`)
var headerAddressPattern = regexp.MustCompile(`\(([^()]+)\)`)

func parseTraceroute(output, requestedDestination string) (Topology, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return Topology{}, fmt.Errorf("traceroute output did not contain any hops")
	}
	destination := requestedDestination
	if match := headerAddressPattern.FindStringSubmatch(lines[0]); len(match) == 2 {
		destination = match[1]
	}
	topology := Topology{Nodes: []TopologyNode{{ID: "hop-0", Hop: 0, Address: "local", Status: "healthy"}}}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hop, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		node := TopologyNode{ID: fmt.Sprintf("hop-%d", hop), Hop: hop, Status: "unknown"}
		if fields[1] != "*" {
			node.Address = strings.Trim(fields[1], "()")
			if net.ParseIP(node.Address) == nil && len(fields) > 2 {
				candidate := strings.Trim(fields[2], "()")
				if net.ParseIP(candidate) != nil {
					node.Address = candidate
				}
			}
			if match := latencyPattern.FindStringSubmatch(line); len(match) == 2 {
				node.LatencyMS, _ = strconv.ParseFloat(match[1], 64)
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

func classifyTopology(topology *Topology) {
	for i := 1; i < len(topology.Nodes); i++ {
		previous, current := topology.Nodes[i-1], &topology.Nodes[i]
		link := TopologyLink{From: previous.ID, To: current.ID, Status: current.Status}
		if current.Status == "healthy" && previous.Status == "healthy" && previous.Hop > 0 {
			link.LatencyDeltaMS = math.Round((current.LatencyMS-previous.LatencyMS)*100) / 100
			if link.LatencyDeltaMS >= latencyJumpThresholdMS {
				link.Status = "degraded"
				current.Status = "degraded"
			}
		}
		topology.Links = append(topology.Links, link)
	}
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
