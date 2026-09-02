//go:build windows

package diagnostic

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"unsafe"
)

func TestWindowsJobLimitEnablesKillOnClose(t *testing.T) {
	info := windowsJobLimitInformation()
	if got := binary.LittleEndian.Uint32(info[windowsJobLimitFlagsOffset:]); got&jobObjectLimitKillOnJobClose == 0 {
		t.Fatalf("limit flags %#x do not enable KILL_ON_JOB_CLOSE", got)
	}
}

func TestWindowsJobLimitInformationUsesDocumentedABISize(t *testing.T) {
	want := uintptr(144)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 112
	}
	info := windowsJobLimitInformation()
	if got := unsafe.Sizeof(info); got != want {
		t.Fatalf("job information size = %d, want %d", got, want)
	}
	if windowsJobLimitFlagsOffset != 16 {
		t.Fatalf("LimitFlags offset = %d, want 16", windowsJobLimitFlagsOffset)
	}
}

func TestConfigureWindowsTraceCommandStartsSuspended(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x200}
	configureWindowsSuspendedStart(cmd)
	if cmd.SysProcAttr.CreationFlags&createSuspended == 0 {
		t.Fatalf("creation flags %#x do not include CREATE_SUSPENDED", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&0x200 == 0 {
		t.Fatalf("creation flags %#x lost existing flags", cmd.SysProcAttr.CreationFlags)
	}
}

func TestWindowsAssignmentAndResumeHelperShapesCompile(t *testing.T) {
	var assign func(syscall.Handle, uint32) error = assignWindowsProcessToJob
	var primaryThread func(uint32) (syscall.Handle, error) = openWindowsPrimaryThread
	var resume func(syscall.Handle) error = resumeWindowsThread
	var attach func(*windowsTraceCommandOwner, *os.Process) error = (*windowsTraceCommandOwner).attach
	if assign == nil || primaryThread == nil || resume == nil || attach == nil {
		t.Fatal("Windows assignment/resume helper unexpectedly nil")
	}
}

func TestPrepareWindowsTraceCommandPreservesDirectChildCancellation(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd.exe", "/c", "exit", "0")
	if cmd.Cancel == nil {
		t.Fatal("CommandContext did not install direct-child cancellation")
	}
	owner, err := prepareTraceCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.cleanup()
	if cmd.Cancel == nil {
		t.Fatal("job ownership removed direct-child cancellation")
	}
}
