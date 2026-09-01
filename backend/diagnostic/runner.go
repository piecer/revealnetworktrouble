package diagnostic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTimeout       = 5 * time.Second
	MinTimeout           = 100 * time.Millisecond
	MaxTimeout           = 30 * time.Second
	MaxTargets           = 20
	DefaultTraceAttempts = 5
	MaxTraceAttempts     = 10
)

type Checker interface {
	Kind() Kind
	Check(context.Context, Target) Result
}

type Runner struct {
	checkers map[Kind]Checker
}

func NewRunner(checkers ...Checker) *Runner {
	r := &Runner{checkers: make(map[Kind]Checker, len(checkers))}
	for _, checker := range checkers {
		r.checkers[checker.Kind()] = checker
	}
	return r
}

func (r *Runner) Validate(req Request) error {
	if len(req.Targets) < 1 || len(req.Targets) > MaxTargets {
		return fmt.Errorf("targets must contain 1 to %d items", MaxTargets)
	}
	if req.TimeoutMS != 0 {
		t := time.Duration(req.TimeoutMS) * time.Millisecond
		if t < MinTimeout || t > MaxTimeout {
			return fmt.Errorf("timeout_ms must be between %d and %d", MinTimeout.Milliseconds(), MaxTimeout.Milliseconds())
		}
	}
	for i, target := range req.Targets {
		if strings.TrimSpace(target.Address) == "" {
			return fmt.Errorf("targets[%d].address is required", i)
		}
		if _, ok := r.checkers[target.Kind]; !ok {
			return fmt.Errorf("targets[%d].kind is unsupported", i)
		}
		if target.Attempts < 0 || target.Attempts > MaxTraceAttempts {
			return fmt.Errorf("targets[%d].attempts must be between 1 and %d", i, MaxTraceAttempts)
		}
		if target.Attempts != 0 && target.Kind != KindTraceroute {
			return fmt.Errorf("targets[%d].attempts is only supported for traceroute", i)
		}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, req Request) (Report, error) {
	if err := r.Validate(req); err != nil {
		return Report{}, err
	}
	timeout := DefaultTimeout
	if req.TimeoutMS != 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	started := time.Now().UTC()
	results := make([]Result, len(req.Targets))
	var wg sync.WaitGroup
	for i, target := range req.Targets {
		wg.Add(1)
		go func(i int, target Target) {
			defer wg.Done()
			checkTimeout := timeout
			if target.Kind == KindTraceroute {
				attempts := target.Attempts
				if attempts == 0 {
					attempts = DefaultTraceAttempts
				}
				checkTimeout *= time.Duration(attempts)
			}
			checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			results[i] = r.checkers[target.Kind].Check(checkCtx, target)
		}(i, target)
	}
	wg.Wait()

	report := Report{ID: newID(), StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Results: results}
	hasDegraded := false
	for _, result := range results {
		report.Summary.Total++
		if result.Status == StatusHealthy {
			report.Summary.Passed++
		} else {
			report.Summary.Failed++
		}
		if result.Status == StatusDegraded {
			hasDegraded = true
		}
	}
	switch {
	case report.Summary.Failed == 0:
		report.Status = StatusHealthy
	case hasDegraded || report.Summary.Passed > 0:
		report.Status = StatusDegraded
	default:
		report.Status = StatusUnreachable
	}
	return report, nil
}

func baseResult(kind Kind, address string, started time.Time, err error) Result {
	result := Result{Kind: kind, Address: address, StartedAt: started, LatencyMS: time.Since(started).Milliseconds(), Status: StatusHealthy}
	if err == nil {
		return result
	}
	result.Status = StatusUnreachable
	result.Message = err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.ErrorCode = "timeout"
	case errors.Is(err, context.Canceled):
		result.ErrorCode = "cancelled"
	default:
		result.ErrorCode = "connection_failed"
	}
	return result
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("report-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
