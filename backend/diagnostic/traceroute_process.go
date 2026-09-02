package diagnostic

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const traceCommandWaitDelay = 200 * time.Millisecond

type traceCommandOwner interface {
	attach(*os.Process) error
	cleanup() error
}

type boundedTraceOutput struct {
	mu              sync.Mutex
	bytes           []byte
	overflow        bool
	cancel          func()
	cancelTriggered atomic.Bool
}

func (output *boundedTraceOutput) Write(p []byte) (int, error) {
	output.mu.Lock()
	remaining := MaxTraceOutputBytes - len(output.bytes)
	if remaining > 0 {
		kept := min(remaining, len(p))
		output.bytes = append(output.bytes, p[:kept]...)
	}
	overflow := len(p) > remaining
	if overflow {
		output.overflow = true
	}
	output.mu.Unlock()

	if overflow && output.cancelTriggered.CompareAndSwap(false, true) {
		output.cancel()
	}
	return len(p), nil
}

func (output *boundedTraceOutput) result() ([]byte, bool) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.bytes...), output.overflow
}

func runTraceCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	owner, err := prepareTraceCommand(cmd)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, err
	}
	cmd.WaitDelay = traceCommandWaitDelay

	output := &boundedTraceOutput{cancel: func() {
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		}
	}}
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		cleanupErr := owner.cleanup()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, errors.Join(contextErr, cleanupErr)
		}
		return nil, errors.Join(err, cleanupErr)
	}
	if err := owner.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cleanupErr := owner.cleanup()
		prefix, overflow := output.result()
		if contextErr := ctx.Err(); contextErr != nil {
			return prefix, errors.Join(contextErr, cleanupErr)
		}
		if overflow {
			return prefix, errors.Join(ErrTraceOutputLimit, cleanupErr)
		}
		return prefix, errors.Join(err, cleanupErr)
	}

	waitErr := cmd.Wait()
	cleanupErr := owner.cleanup()
	prefix, overflow := output.result()
	return prefix, traceCommandResultError(ctx.Err(), overflow, waitErr, cleanupErr)
}

func traceCommandResultError(contextErr error, overflow bool, waitErr, cleanupErr error) error {
	primaryErr := waitErr
	if overflow {
		primaryErr = ErrTraceOutputLimit
	}
	if contextErr != nil {
		primaryErr = contextErr
	}
	return errors.Join(primaryErr, cleanupErr)
}
