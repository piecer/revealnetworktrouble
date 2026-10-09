package api

import "github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"

func marshalReportResponse(report diagnostic.Report, mode diagnostic.TopologyMode, geoDetails bool) ([]byte, error) {
	// Never trust a pre-attached extension as negotiation. Collect raw snapshots
	// once before compact pruning; default transport remains byte-identical.
	report.GeoDetails = nil
	var raw *diagnostic.GeoDetailsSidecar
	if geoDetails {
		raw = diagnostic.BuildGeoDetails(report)
	}
	winner := report
	var legacy []byte
	var err error
	limit := maxFullResponseBytes
	if mode == diagnostic.TopologyModeCompact {
		limit = diagnostic.CompactTopologyMaxResponseBytes - 1
		legacy, err = marshalCompactResponseRetainingSnapshot(report, func(value diagnostic.Report) ([]byte, error) {
			return marshalClosedResponse(value, diagnostic.CompactTopologyMaxResponseBytes-1, false)
		}, diagnostic.BuildCompactTopologyWithOptions, &winner)
	} else {
		legacy, err = marshalFullResponse(report)
	}
	if err != nil || raw == nil {
		return legacy, err
	}
	return fitGeoDetails(winner, legacy, raw, func(value diagnostic.Report) ([]byte, error) {
		return marshalClosedResponse(value, limit, true)
	})
}

// fitGeoDetails adds only a complete prefix to an already fitted legacy
// snapshot. All closed serializer resource limits still apply on every probe.
// Zero fitting entries is distinct from an envelope that cannot fit at all.
func fitGeoDetails(report diagnostic.Report, legacy []byte, raw *diagnostic.GeoDetailsSidecar, marshal compactReportMarshaler) ([]byte, error) {
	best := legacy
	low, high := 0, min(len(raw.Entries), diagnostic.GeoDetailsMaxEntries)
	for low <= high {
		count := low + (high-low)/2
		candidate := *raw
		candidate.Entries = raw.Entries[:count]
		candidate.Omitted = raw.Total - count
		_, err := marshalGeoDetailsSidecar(candidate)
		var payload []byte
		if err == nil {
			report.GeoDetails = &candidate
			payload, err = marshal(report)
		}
		if err != nil {
			if !isFullResponseBudgetError(err) {
				return nil, err
			}
			high = count - 1
		} else {
			best = payload
			low = count + 1
		}
	}
	return best, nil
}
