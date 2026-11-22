package diagnostic

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

// stageTimingObserver records per-phase durations from httptrace events. Only
// phases that actually ran are recorded: reused connections never fire
// ConnectStart, cached lookups never fire DNSStart, and failed phases have no
// duration to report, so absent keys mean "not observed", never zero.
type stageTimingObserver struct {
	mu        sync.Mutex
	now       func() time.Time
	starts    map[string]time.Time
	abandoned map[string]bool
	durations map[string]int64
}

func newStageTimingObserver(now func() time.Time) *stageTimingObserver {
	clock := now
	if clock == nil {
		clock = time.Now
	}
	return &stageTimingObserver{
		now:       clock,
		starts:    map[string]time.Time{},
		abandoned: map[string]bool{},
		durations: map[string]int64{},
	}
}

func (o *stageTimingObserver) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) {
			o.begin(ResultDetailDNSPhaseMS)
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			o.complete(ResultDetailDNSPhaseMS, info.Err)
		},
		ConnectStart: func(_, _ string) {
			o.begin(ResultDetailConnectPhaseMS)
		},
		ConnectDone: func(_, _ string, err error) {
			o.complete(ResultDetailConnectPhaseMS, err)
		},
		TLSHandshakeStart: func() {
			o.begin(ResultDetailTLSPhaseMS)
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			o.complete(ResultDetailTLSPhaseMS, err)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				o.abandon(ResultDetailTTFBPhaseMS)
				return
			}
			o.begin(ResultDetailTTFBPhaseMS)
		},
		GotFirstResponseByte: func() {
			o.complete(ResultDetailTTFBPhaseMS, nil)
		},
	}
}

func (o *stageTimingObserver) begin(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, exists := o.starts[key]; exists {
		return
	}
	o.starts[key] = o.now()
}

func (o *stageTimingObserver) complete(key string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil || o.abandoned[key] {
		return
	}
	start, ok := o.starts[key]
	if !ok {
		return
	}
	o.durations[key] = o.now().Sub(start).Milliseconds()
}

func (o *stageTimingObserver) abandon(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.abandoned[key] = true
}

func (o *stageTimingObserver) ObservedDurations() map[string]int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	observed := make(map[string]int64, len(o.durations))
	for key, duration := range o.durations {
		observed[key] = duration
	}
	return observed
}
