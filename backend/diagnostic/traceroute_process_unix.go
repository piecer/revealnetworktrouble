//go:build !windows

package diagnostic

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixTraceCommandOwner struct {
	pid int
}

func prepareTraceCommand(cmd *exec.Cmd) (traceCommandOwner, error) {
	owner := &unixTraceCommandOwner{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return killTraceProcessGroup(cmd.Process.Pid)
	}
	return owner, nil
}

func (owner *unixTraceCommandOwner) attach(process *os.Process) error {
	owner.pid = process.Pid
	return nil
}

func (owner *unixTraceCommandOwner) cleanup() error {
	if owner.pid == 0 {
		return nil
	}
	return killTraceProcessGroup(owner.pid)
}

func killTraceProcessGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
