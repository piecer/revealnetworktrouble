package diagnostic

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedTraceOutputReentrantCancelWriteDoesNotDeadlockAndCancelsOnce(t *testing.T) {
	var cancelCalls atomic.Int32
	var output *boundedTraceOutput
	output = &boundedTraceOutput{cancel: func() {
		cancelCalls.Add(1)
		_, _ = output.Write([]byte("reentrant overflow"))
	}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = output.Write(make([]byte, MaxTraceOutputBytes+1))
		_, _ = output.Write([]byte("later overflow"))
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reentrant cancel callback deadlocked Write")
	}
	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("cancel calls = %d, want 1", got)
	}
}

func TestTraceCommandResultErrorPreservesCleanupWithPrimaryPrecedence(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	waitErr := errors.New("wait failed")
	tests := []struct {
		name       string
		contextErr error
		overflow   bool
		waitErr    error
		primary    error
	}{
		{name: "context", contextErr: context.Canceled, overflow: true, waitErr: waitErr, primary: context.Canceled},
		{name: "output", overflow: true, waitErr: waitErr, primary: ErrTraceOutputLimit},
		{name: "wait", waitErr: waitErr, primary: waitErr},
		{name: "cleanup only", primary: cleanupErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := traceCommandResultError(tt.contextErr, tt.overflow, tt.waitErr, cleanupErr)
			if !errors.Is(err, tt.primary) {
				t.Fatalf("error = %v, want primary %v", err, tt.primary)
			}
			if !errors.Is(err, cleanupErr) {
				t.Fatalf("error = %v, want joined cleanup %v", err, cleanupErr)
			}
			if got := strings.Split(err.Error(), "\n")[0]; got != tt.primary.Error() {
				t.Fatalf("first error = %q, want %q", got, tt.primary)
			}
		})
	}
}

func TestRunTraceCommandStartFailureIsBounded(t *testing.T) {
	started := time.Now()
	_, err := runTraceCommand(context.Background(), "checknetwork-command-that-does-not-exist")
	if err == nil {
		t.Fatal("expected start failure")
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		t.Fatalf("error = %T %v, want *exec.Error", err, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("start failure took %s", elapsed)
	}
}

func TestRunTraceCommandContextPrecedesStartFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := runTraceCommand(ctx, "checknetwork-command-that-does-not-exist")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("pre-canceled start took %s", elapsed)
	}
}
