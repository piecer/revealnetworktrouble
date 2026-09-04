package diagnostic

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strings"
	"unicode/utf8"
)

type CompactTopologySchema string
type CompactTopologySelection string

const (
	CompactTopologySchemaV1    CompactTopologySchema    = "compact-v1"
	CompactTopologySelectionV1 CompactTopologySelection = "fair-complete-prefix-v1"

	CompactTopologyMaxNodes = 500
	CompactTopologyMaxLinks = 1000
	// CompactTopologyMaxRouteNodes covers local + 30 observed hops + the
	// synthetic destination emitted when the trace does not reach its target.
	CompactTopologyMaxRouteNodes     = MaxTraceHops + 2
	CompactTopologyMaxResponseBytes  = 1 << 20
	CompactTopologyMaxGeoBundleBytes = 4096
	// CompactTopologyMaxGeoStringBytes bounds each provider-controlled text
	// field before the complete Geo/ASN bundle is considered for projection.
	CompactTopologyMaxGeoStringBytes = CompactTopologyMaxGeoBundleBytes
)

type CompactNodeKind string

const (
	CompactNodeLocal    CompactNodeKind = "local"
	CompactNodeIP       CompactNodeKind = "ip"
	CompactNodeHostname CompactNodeKind = "hostname"
	CompactNodeUnknown  CompactNodeKind = "unknown"
)

type CompactTruncationReason string

const (
	CompactTruncationNodeLimit    CompactTruncationReason = "node_limit"
	CompactTruncationLinkLimit    CompactTruncationReason = "link_limit"
	CompactTruncationGeoLimit     CompactTruncationReason = "geo_metadata_limit"
	CompactTruncationResponseSize CompactTruncationReason = "response_size"
)

type CompactTopologyLimits struct {
	Nodes                     int `json:"nodes"`
	Links                     int `json:"links"`
	MaxResponseBytesExclusive int `json:"max_response_bytes_exclusive"`
	MaxGeoBundleBytes         int `json:"max_geo_bundle_bytes"`
}

type CompactTopology struct {
	Schema            CompactTopologySchema     `json:"schema"`
	Selection         CompactTopologySelection  `json:"selection"`
	Limits            CompactTopologyLimits     `json:"limits,omitempty"`
	Nodes             []CompactTopologyNode     `json:"nodes"`
	Links             []CompactTopologyLink     `json:"links"`
	Routes            []CompactTopologyRoute    `json:"routes"`
	Stats             CompactTopologyStats      `json:"stats,omitempty"`
	ResultStats       []CompactResultStats      `json:"result_stats,omitempty"`
	Geo               CompactGeoStats           `json:"geo,omitempty"`
	Truncated         bool                      `json:"truncated"`
	TruncationReasons []CompactTruncationReason `json:"truncation_reasons,omitempty"`
}

type CompactTopologyNode struct {
	ID           string          `json:"id"`
	Kind         CompactNodeKind `json:"kind"`
	Address      string          `json:"address,omitempty"`
	Status       string          `json:"status"`
	HopMin       int             `json:"hop_min"`
	HopMax       int             `json:"hop_max"`
	LatencyMSAvg *float64        `json:"latency_ms_avg,omitempty"`
	Observations int             `json:"observations"`
	PublicIP     bool            `json:"public_ip,omitempty"`
	Geolocation  *GeoLocation    `json:"geolocation,omitempty"`
	ASN          *ASNInfo        `json:"asn,omitempty"`
}

type CompactTopologyLink struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Status       string `json:"status"`
	Observations int    `json:"observations"`
}

type CompactTopologyRoute struct {
	ResultIndex int      `json:"result_index"`
	Attempt     int      `json:"attempt"`
	Status      Status   `json:"status"`
	Reached     bool     `json:"reached"`
	Complete    bool     `json:"complete"`
	NodeIDs     []string `json:"node_ids"`
}

type CompactCountStats struct {
	Total     int `json:"total"`
	Displayed int `json:"displayed"`
	Omitted   int `json:"omitted"`
}

type CompactRouteStats struct {
	Total     int `json:"total"`
	Displayed int `json:"displayed"`
	Complete  int `json:"complete"`
	Partial   int `json:"partial"`
	Omitted   int `json:"omitted"`
}

type CompactTopologyStats struct {
	Nodes            CompactCountStats `json:"nodes"`
	Links            CompactCountStats `json:"links"`
	Routes           CompactRouteStats `json:"routes"`
	NodeObservations CompactCountStats `json:"node_observations"`
	LinkObservations CompactCountStats `json:"link_observations"`
}

type CompactResultStats struct {
	ResultIndex      int               `json:"result_index"`
	Routes           CompactRouteStats `json:"routes"`
	NodeObservations CompactCountStats `json:"node_observations"`
	LinkObservations CompactCountStats `json:"link_observations"`
}

type CompactGeoStats struct {
	Eligible    int `json:"eligible"`
	Available   int `json:"available"`
	Included    int `json:"included"`
	Omitted     int `json:"omitted"`
	Unavailable int `json:"unavailable"`
}

type compactBuildOptions struct {
	maxNodes                int
	maxLinks                int
	maxAcceptedTransactions int
	includeGeo              bool
}

type CompactTopologyBuildResult struct {
	Topology             *CompactTopology `json:"topology"`
	AcceptedTransactions int              `json:"-"`
}

type compactBuildResult = CompactTopologyBuildResult

// CloneCompactTopology returns a deep copy suitable for response-size probes.
// Probe mutations cannot affect the runner's cached topology or raw report.
func CloneCompactTopology(source *CompactTopology) *CompactTopology {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Nodes = cloneSlicePreservingNil(source.Nodes)
	for index := range clone.Nodes {
		node := &clone.Nodes[index]
		if node.LatencyMSAvg != nil {
			latency := *node.LatencyMSAvg
			node.LatencyMSAvg = &latency
		}
		node.Geolocation = cloneGeoLocation(node.Geolocation)
		node.ASN = cloneASNInfo(node.ASN)
	}
	clone.Links = cloneSlicePreservingNil(source.Links)
	clone.Routes = cloneSlicePreservingNil(source.Routes)
	for index := range clone.Routes {
		clone.Routes[index].NodeIDs = cloneSlicePreservingNil(source.Routes[index].NodeIDs)
	}
	clone.ResultStats = append([]CompactResultStats(nil), source.ResultStats...)
	clone.TruncationReasons = append([]CompactTruncationReason(nil), source.TruncationReasons...)
	return &clone
}

func cloneSlicePreservingNil[T any](source []T) []T {
	if source == nil {
		return nil
	}
	clone := make([]T, len(source))
	copy(clone, source)
	return clone
}

type compactNodeObservation struct {
	key     string
	kind    CompactNodeKind
	address string
	raw     []TopologyNode
}

type compactEdgeKey struct {
	from string
	to   string
}

type compactRawRoute struct {
	resultIndex  int
	attempt      int
	occurrence   int
	status       Status
	reached      bool
	nodes        []compactNodeObservation
	edges        []compactEdgeKey
	edgeStatuses []string
	accepted     int
	blocked      bool
}

type compactNodeAggregate struct {
	kind         CompactNodeKind
	address      string
	status       string
	hopMin       int
	hopMax       int
	observations int
	latencyTotal float64
	latencyCount int
	publicIP     bool
	geolocation  *GeoLocation
	asn          *ASNInfo
}

type compactLinkAggregate struct {
	status       string
	observations int
}

// BuildCompactTopology creates a deterministic compact projection without
// changing the report or any of its raw traceroute facts.
func BuildCompactTopology(report Report) *CompactTopology {
	return BuildCompactTopologyWithOptions(report, -1, true).Topology
}

// BuildCompactReport returns the compact transport view without modifying raw
// results used by analysis or legacy/full responses.
func BuildCompactReport(report Report, topology *CompactTopology) Report {
	compact := report
	compact.Results = cloneSlicePreservingNil(report.Results)
	for index := range compact.Results {
		result := &compact.Results[index]
		if result.Kind != KindTraceroute || result.Details == nil {
			continue
		}
		details := make(map[string]any, len(result.Details))
		for key, value := range result.Details {
			if key != "attempts" && key != "topology" {
				details[key] = value
			}
		}
		result.Details = details
	}
	compact.CompactTopology = topology
	return compact
}

// BuildCompactTopologyWithOptions supports deterministic response-size
// rollback. maxAcceptedTransactions below zero means no transaction bound.
func BuildCompactTopologyWithOptions(report Report, maxAcceptedTransactions int, includeGeo bool) CompactTopologyBuildResult {
	return buildCompactTopology(report, compactBuildOptions{maxAcceptedTransactions: maxAcceptedTransactions, includeGeo: includeGeo})
}

func buildCompactTopology(report Report, options compactBuildOptions) compactBuildResult {
	maxNodes := options.maxNodes
	if maxNodes <= 0 {
		maxNodes = CompactTopologyMaxNodes
	}
	maxLinks := options.maxLinks
	if maxLinks <= 0 {
		maxLinks = CompactTopologyMaxLinks
	}

	routes := compactRoutes(report)
	fullNodes := make(map[string]struct{})
	fullLinks := make(map[compactEdgeKey]struct{})
	resultStats := make([]CompactResultStats, len(report.Results))
	for i := range resultStats {
		resultStats[i].ResultIndex = i
	}
	for _, route := range routes {
		resultStats[route.resultIndex].Routes.Total++
		resultStats[route.resultIndex].NodeObservations.Total += len(route.nodes)
		resultStats[route.resultIndex].LinkObservations.Total += compactNonSelfEdgeCount(route.edges, len(route.edges))
		for _, observation := range route.nodes {
			fullNodes[observation.key] = struct{}{}

		}
		for _, edge := range route.edges {
			if edge.from != edge.to {
				fullLinks[edge] = struct{}{}
			}
		}
	}

	selectedNodes := make(map[string]struct{})
	selectedLinks := make(map[compactEdgeKey]struct{})
	selectedOrder := make([]string, 0, min(maxNodes, len(fullNodes)))
	addSelectedNode := func(key string) {
		if _, exists := selectedNodes[key]; exists {
			return
		}
		selectedNodes[key] = struct{}{}
		selectedOrder = append(selectedOrder, key)
	}
	for _, route := range routes {
		if len(route.nodes) > 0 && route.nodes[0].kind == CompactNodeLocal {
			addSelectedNode(route.nodes[0].key)
			break
		}
	}

	groups := make([][]int, len(report.Results))
	for routeIndex := range routes {
		groups[routes[routeIndex].resultIndex] = append(groups[routes[routeIndex].resultIndex], routeIndex)
	}
	cursors := make([]int, len(groups))
	nodeLimited, linkLimited := false, false
	acceptedTransactions := 0
	for {
		active := false
		for resultIndex, group := range groups {
			if len(group) == 0 {
				continue
			}
			chosen := -1
			for scanned := 0; scanned < len(group); scanned++ {
				position := (cursors[resultIndex] + scanned) % len(group)
				routeIndex := group[position]
				route := &routes[routeIndex]
				if !route.blocked && route.accepted < len(route.edges) {
					chosen = routeIndex
					cursors[resultIndex] = (position + 1) % len(group)
					break
				}
			}
			if chosen < 0 {
				continue
			}
			active = true
			if options.maxAcceptedTransactions >= 0 && acceptedTransactions >= options.maxAcceptedTransactions {
				continue
			}
			route := &routes[chosen]
			edge := route.edges[route.accepted]
			nodeCost := 0
			if _, exists := selectedNodes[edge.from]; !exists {
				nodeCost++
			}
			if edge.to != edge.from {
				if _, exists := selectedNodes[edge.to]; !exists {
					nodeCost++
				}
			}
			linkCost := 0
			if edge.from != edge.to {
				if _, exists := selectedLinks[edge]; !exists {
					linkCost = 1
				}
			}
			if len(selectedNodes)+nodeCost > maxNodes {
				route.blocked = true
				nodeLimited = true
				continue
			}
			if len(selectedLinks)+linkCost > maxLinks {
				route.blocked = true
				linkLimited = true
				continue
			}
			addSelectedNode(edge.from)
			addSelectedNode(edge.to)
			if edge.from != edge.to {
				selectedLinks[edge] = struct{}{}
			}
			route.accepted++
			acceptedTransactions++
		}
		if !active || (options.maxAcceptedTransactions >= 0 && acceptedTransactions >= options.maxAcceptedTransactions) {
			break
		}
	}

	nodeAggregates := make(map[string]*compactNodeAggregate, len(selectedNodes))
	linkAggregates := make(map[compactEdgeKey]*compactLinkAggregate, len(selectedLinks))
	displayedRoutes := make([]*compactRawRoute, 0, len(routes))
	for routeIndex := range routes {
		route := &routes[routeIndex]
		nodeCount := 0
		complete := route.accepted == len(route.edges)
		if route.accepted > 0 {
			nodeCount = route.accepted + 1
		} else if complete && len(route.nodes) == 1 {
			if _, selected := selectedNodes[route.nodes[0].key]; selected {
				nodeCount = 1
			}
		}
		if nodeCount == 0 {
			continue
		}
		displayedRoutes = append(displayedRoutes, route)
		stats := &resultStats[route.resultIndex]
		stats.Routes.Displayed++
		if complete {
			stats.Routes.Complete++
		} else {
			stats.Routes.Partial++
		}
		stats.NodeObservations.Displayed += nodeCount
		stats.LinkObservations.Displayed += compactNonSelfEdgeCount(route.edges, route.accepted)
		for i := 0; i < nodeCount; i++ {
			observation := route.nodes[i]
			for _, raw := range observation.raw {
				aggregate := nodeAggregates[observation.key]
				if aggregate == nil {
					aggregate = &compactNodeAggregate{kind: observation.kind, address: observation.address, status: compactNormalizedTopologyStatus(raw.Status), hopMin: raw.Hop, hopMax: raw.Hop}
					nodeAggregates[observation.key] = aggregate
				}
				aggregate.status = compactWorstStatus(aggregate.status, compactNormalizedTopologyStatus(raw.Status))
				aggregate.hopMin = min(aggregate.hopMin, raw.Hop)
				aggregate.hopMax = max(aggregate.hopMax, raw.Hop)
				aggregate.observations++
				aggregate.publicIP = aggregate.publicIP || raw.PublicIP
				asn := canonicalASNInfo(raw.ASN)
				if aggregate.geolocation == nil && aggregate.asn == nil && (raw.Geolocation != nil || asn != nil) {
					aggregate.geolocation = raw.Geolocation
					aggregate.asn = asn
				}
				if raw.Hop > 0 && raw.LatencyMS >= 0 && !math.IsNaN(raw.LatencyMS) && !math.IsInf(raw.LatencyMS, 0) {
					aggregate.latencyTotal += raw.LatencyMS
					aggregate.latencyCount++
				}
			}
		}
		for i := 0; i < route.accepted; i++ {
			edge := route.edges[i]
			if edge.from == edge.to {
				continue
			}
			aggregate := linkAggregates[edge]
			status := route.edgeStatuses[i]
			if aggregate == nil {
				aggregate = &compactLinkAggregate{status: status}
				linkAggregates[edge] = aggregate
			}
			aggregate.status = compactWorstStatus(aggregate.status, status)
			aggregate.observations++
		}
	}

	ids := make(map[string]string, len(selectedOrder))
	nodes := make([]CompactTopologyNode, 0, len(selectedOrder))
	geo := CompactGeoStats{}
	geoLimited := false
	for _, key := range selectedOrder {
		aggregate := nodeAggregates[key]
		if aggregate == nil {
			continue
		}
		id := fmt.Sprintf("n%06d", len(nodes)+1)
		ids[key] = id
		node := CompactTopologyNode{ID: id, Kind: aggregate.kind, Address: aggregate.address, Status: aggregate.status, HopMin: aggregate.hopMin, HopMax: aggregate.hopMax, Observations: aggregate.observations, PublicIP: aggregate.publicIP}
		if aggregate.latencyCount > 0 {
			average := aggregate.latencyTotal / float64(aggregate.latencyCount)
			if !math.IsNaN(average) && !math.IsInf(average, 0) {
				node.LatencyMSAvg = &average
			}
		}
		if node.PublicIP {
			geo.Eligible++
			if aggregate.geolocation != nil || aggregate.asn != nil {
				if !compactGeoBundleValid(aggregate.geolocation, aggregate.asn) {
					geo.Unavailable++
				} else if options.includeGeo && compactGeoBundleContribution(node, aggregate.geolocation, aggregate.asn) <= CompactTopologyMaxGeoBundleBytes {
					geo.Available++
					node.Geolocation = cloneGeoLocation(aggregate.geolocation)
					node.ASN = cloneASNInfo(aggregate.asn)
					geo.Included++
				} else {
					geo.Available++
					geo.Omitted++
					geoLimited = true
				}
			} else {
				geo.Unavailable++
			}
		}
		nodes = append(nodes, node)
	}

	links := make([]CompactTopologyLink, 0, len(linkAggregates))
	for _, fromKey := range selectedOrder {
		for _, toKey := range selectedOrder {
			edge := compactEdgeKey{from: fromKey, to: toKey}
			aggregate := linkAggregates[edge]
			if aggregate == nil {
				continue
			}
			links = append(links, CompactTopologyLink{From: ids[fromKey], To: ids[toKey], Status: aggregate.status, Observations: aggregate.observations})
		}
	}

	compactRoutes := make([]CompactTopologyRoute, 0, len(displayedRoutes))
	for _, route := range displayedRoutes {
		nodeCount := route.accepted + 1
		if len(route.edges) == 0 {
			nodeCount = 1
		}
		nodeIDs := make([]string, nodeCount)
		for i := 0; i < nodeCount; i++ {
			nodeIDs[i] = ids[route.nodes[i].key]
		}
		compactRoutes = append(compactRoutes, CompactTopologyRoute{ResultIndex: route.resultIndex, Attempt: route.attempt, Status: route.status, Reached: route.reached, Complete: route.accepted == len(route.edges), NodeIDs: nodeIDs})
	}

	stats := CompactTopologyStats{
		Nodes:            CompactCountStats{Total: len(fullNodes), Displayed: len(nodes)},
		Links:            CompactCountStats{Total: len(fullLinks), Displayed: len(links)},
		Routes:           CompactRouteStats{Total: len(routes), Displayed: len(compactRoutes)},
		NodeObservations: CompactCountStats{Displayed: sumDisplayedNodeObservations(resultStats)},
		LinkObservations: CompactCountStats{Displayed: sumDisplayedLinkObservations(resultStats)},
	}
	for i := range resultStats {
		stats.NodeObservations.Total += resultStats[i].NodeObservations.Total
		stats.LinkObservations.Total += resultStats[i].LinkObservations.Total
		stats.Routes.Complete += resultStats[i].Routes.Complete
		stats.Routes.Partial += resultStats[i].Routes.Partial
		finishCompactCount(&resultStats[i].NodeObservations)
		finishCompactCount(&resultStats[i].LinkObservations)
		resultStats[i].Routes.Omitted = resultStats[i].Routes.Total - resultStats[i].Routes.Displayed
	}
	finishCompactCount(&stats.Nodes)
	finishCompactCount(&stats.Links)
	finishCompactCount(&stats.NodeObservations)
	finishCompactCount(&stats.LinkObservations)
	stats.Routes.Omitted = stats.Routes.Total - stats.Routes.Displayed

	reasons := make([]CompactTruncationReason, 0, 3)
	if nodeLimited {
		reasons = append(reasons, CompactTruncationNodeLimit)
	}
	if linkLimited {
		reasons = append(reasons, CompactTruncationLinkLimit)
	}
	if geoLimited {
		reasons = append(reasons, CompactTruncationGeoLimit)
	}
	topology := &CompactTopology{
		Schema: CompactTopologySchemaV1, Selection: CompactTopologySelectionV1,
		Limits: CompactTopologyLimits{Nodes: CompactTopologyMaxNodes, Links: CompactTopologyMaxLinks, MaxResponseBytesExclusive: CompactTopologyMaxResponseBytes, MaxGeoBundleBytes: CompactTopologyMaxGeoBundleBytes},
		Nodes:  nodes, Links: links, Routes: compactRoutes, Stats: stats, ResultStats: resultStats, Geo: geo,
		Truncated: len(reasons) > 0, TruncationReasons: reasons,
	}
	return compactBuildResult{Topology: topology, AcceptedTransactions: acceptedTransactions}
}

func compactRoutes(report Report) []compactRawRoute {
	routes := make([]compactRawRoute, 0)
	for resultIndex, result := range report.Results {
		if result.Kind != KindTraceroute || result.Details == nil {
			continue
		}
		attempts, ok := compactTraceAttempts(result.Details["attempts"])
		if !ok {
			continue
		}
		sort.SliceStable(attempts, func(i, j int) bool { return attempts[i].Attempt < attempts[j].Attempt })
		for occurrence, attempt := range attempts {
			if !traceAttemptEligible(attempt) {
				continue
			}
			topologyNodes := append([]TopologyNode(nil), attempt.Topology.Nodes...)
			sort.SliceStable(topologyNodes, func(i, j int) bool { return topologyNodes[i].Hop < topologyNodes[j].Hop })
			route := compactRawRoute{resultIndex: resultIndex, attempt: attempt.Attempt, occurrence: occurrence, status: attempt.Status, reached: attempt.Topology.Reached}
			for nodeIndex, node := range topologyNodes {
				key, kind, address := compactCanonicalNode(node, resultIndex, attempt.Attempt, occurrence, nodeIndex)
				if len(route.nodes) > 0 && route.nodes[len(route.nodes)-1].key == key {
					last := &route.nodes[len(route.nodes)-1]
					last.raw = append(last.raw, node)
					continue
				}
				route.nodes = append(route.nodes, compactNodeObservation{key: key, kind: kind, address: address, raw: []TopologyNode{node}})
			}
			for i := 1; i < len(route.nodes); i++ {
				route.edges = append(route.edges, compactEdgeKey{from: route.nodes[i-1].key, to: route.nodes[i].key})
				from := route.nodes[i-1].raw[len(route.nodes[i-1].raw)-1]
				to := route.nodes[i].raw[0]
				status := compactNormalizedTopologyStatus(to.Status)
				for _, link := range attempt.Topology.Links {
					if link.From == from.ID && link.To == to.ID {
						status = compactNormalizedTopologyStatus(link.Status)
						break
					}
				}
				route.edgeStatuses = append(route.edgeStatuses, status)
			}
			routes = append(routes, route)
		}
	}
	return routes
}

func compactTraceAttempts(value any) ([]TraceAttempt, bool) {
	if value == nil {
		return nil, false
	}
	if attempts, ok := value.([]TraceAttempt); ok {
		return append([]TraceAttempt(nil), attempts...), true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var attempts []TraceAttempt
	if err := json.Unmarshal(encoded, &attempts); err != nil {
		return nil, false
	}
	return attempts, true
}

func compactCanonicalNode(node TopologyNode, resultIndex, attempt, occurrence, nodeIndex int) (string, CompactNodeKind, string) {
	if nodeIndex == 0 && node.Hop == 0 {
		return "local", CompactNodeLocal, "local"
	}
	address := strings.TrimSpace(node.Address)
	if address == "" {
		return fmt.Sprintf("unknown:%d:%d:%d:%d", resultIndex, attempt, occurrence, node.Hop), CompactNodeUnknown, ""
	}
	if parsed, err := netip.ParseAddr(address); err == nil {
		parsed = parsed.Unmap()
		canonical := parsed.String()
		return "ip:" + canonical, CompactNodeIP, canonical
	}
	hostname := strings.ToLower(strings.TrimSuffix(address, "."))
	return "host:" + hostname, CompactNodeHostname, hostname
}

func compactNormalizedTopologyStatus(status string) string {
	switch status {
	case "healthy", "degraded", "unknown", "failure":
		return status
	case string(StatusUnreachable):
		return "failure"
	default:
		return "unknown"
	}
}

func compactWorstStatus(left, right string) string {
	if compactStatusRank(right) > compactStatusRank(left) {
		return right
	}
	if left == "" {
		return right
	}
	return left
}

func compactStatusRank(status string) int {
	switch status {
	case "healthy":
		return 0
	case "degraded":
		return 1
	case "unknown":
		return 2
	case "failure", string(StatusUnreachable):
		return 3
	default:
		return 2
	}
}

func compactGeoBundleContribution(node CompactTopologyNode, geolocation *GeoLocation, asn *ASNInfo) int {
	without, err := json.Marshal(node)
	if err != nil {
		return CompactTopologyMaxGeoBundleBytes + 1
	}
	node.Geolocation = geolocation
	node.ASN = asn
	with, err := json.Marshal(node)
	if err != nil || len(with) < len(without) {
		return CompactTopologyMaxGeoBundleBytes + 1
	}
	return len(with) - len(without)
}

func compactGeoBundleValid(geolocation *GeoLocation, asn *ASNInfo) bool {
	validString := func(value string) bool {
		return len(value) <= CompactTopologyMaxGeoStringBytes && utf8.ValidString(value)
	}
	if geolocation != nil {
		if math.IsNaN(geolocation.Latitude) || math.IsInf(geolocation.Latitude, 0) || geolocation.Latitude < -90 || geolocation.Latitude > 90 ||
			math.IsNaN(geolocation.Longitude) || math.IsInf(geolocation.Longitude, 0) || geolocation.Longitude < -180 || geolocation.Longitude > 180 ||
			!validString(geolocation.City) || !validString(geolocation.Region) || !validString(geolocation.Country) || !validString(geolocation.CountryCode) {
			return false
		}
	}
	if asn != nil && (uint64(asn.Number) > math.MaxUint32 || !validString(asn.Organization)) {
		return false
	}
	return true
}

func cloneGeoLocation(value *GeoLocation) *GeoLocation {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneASNInfo(value *ASNInfo) *ASNInfo {
	value = canonicalASNInfo(value)
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func canonicalASNInfo(value *ASNInfo) *ASNInfo {
	if value == nil || value.Number == 0 && value.Organization == "" {
		return nil
	}
	return value
}

func compactNonSelfEdgeCount(edges []compactEdgeKey, prefix int) int {
	if prefix > len(edges) {
		prefix = len(edges)
	}
	count := 0
	for _, edge := range edges[:prefix] {
		if edge.from != edge.to {
			count++
		}
	}
	return count
}

func finishCompactCount(stats *CompactCountStats) {
	stats.Omitted = stats.Total - stats.Displayed
}

func sumDisplayedNodeObservations(stats []CompactResultStats) int {
	total := 0
	for _, result := range stats {
		total += result.NodeObservations.Displayed
	}
	return total
}

func sumDisplayedLinkObservations(stats []CompactResultStats) int {
	total := 0
	for _, result := range stats {
		total += result.LinkObservations.Displayed
	}
	return total
}
