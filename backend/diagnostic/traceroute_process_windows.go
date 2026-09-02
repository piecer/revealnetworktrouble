//go:build windows

package diagnostic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	windowsJobLimitFlagsOffset        = 16

	createSuspended     = 0x00000004
	processSetQuota     = 0x0100
	processTerminate    = 0x0001
	threadSuspendResume = 0x0002
	threadSnapshot      = 0x00000004
	noMoreFiles         = syscall.Errno(18)
	invalidResumeResult = ^uint32(0)
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procThread32First            = kernel32.NewProc("Thread32First")
	procThread32Next             = kernel32.NewProc("Thread32Next")
	procOpenThread               = kernel32.NewProc("OpenThread")
	procResumeThread             = kernel32.NewProc("ResumeThread")
)

type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePriority   int32
	DeltaPriority  int32
	Flags          uint32
}

type windowsTraceCommandOwner struct {
	job syscall.Handle
}

func windowsJobLimitInformation() windowsJobLimitInformationBuffer {
	var info windowsJobLimitInformationBuffer
	binary.LittleEndian.PutUint32(info[windowsJobLimitFlagsOffset:windowsJobLimitFlagsOffset+4], jobObjectLimitKillOnJobClose)
	return info
}

func configureWindowsSuspendedStart(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
}

func prepareTraceCommand(cmd *exec.Cmd) (traceCommandOwner, error) {
	configureWindowsSuspendedStart(cmd)

	job, _, callErr := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return nil, windowsCallError("CreateJobObjectW", callErr)
	}
	owner := &windowsTraceCommandOwner{job: syscall.Handle(job)}
	info := windowsJobLimitInformation()
	ok, _, callErr := procSetInformationJobObject.Call(
		job,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info[0])),
		uintptr(len(info)),
	)
	if ok == 0 {
		return nil, errors.Join(windowsCallError("SetInformationJobObject", callErr), owner.cleanup())
	}
	return owner, nil
}

func (owner *windowsTraceCommandOwner) attach(process *os.Process) error {
	if err := assignWindowsProcessToJob(owner.job, uint32(process.Pid)); err != nil {
		return err
	}
	thread, err := openWindowsPrimaryThread(uint32(process.Pid))
	if err != nil {
		return err
	}
	resumeErr := resumeWindowsThread(thread)
	closeErr := syscall.CloseHandle(thread)
	if closeErr != nil {
		closeErr = fmt.Errorf("close traceroute primary thread: %w", closeErr)
	}
	return errors.Join(resumeErr, closeErr)
}

func assignWindowsProcessToJob(job syscall.Handle, processID uint32) error {
	processHandle, err := syscall.OpenProcess(processSetQuota|processTerminate, false, processID)
	if err != nil {
		return fmt.Errorf("open traceroute process for job assignment: %w", err)
	}
	defer syscall.CloseHandle(processHandle)

	ok, _, callErr := procAssignProcessToJobObject.Call(uintptr(job), uintptr(processHandle))
	if ok == 0 {
		return windowsCallError("AssignProcessToJobObject", callErr)
	}
	return nil
}

func openWindowsPrimaryThread(processID uint32) (syscall.Handle, error) {
	snapshot, _, callErr := procCreateToolhelp32Snapshot.Call(threadSnapshot, 0)
	if syscall.Handle(snapshot) == syscall.Handle(^uintptr(0)) {
		return 0, windowsCallError("CreateToolhelp32Snapshot", callErr)
	}
	snapshotHandle := syscall.Handle(snapshot)

	var entry threadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	ok, _, callErr := procThread32First.Call(uintptr(snapshotHandle), uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.OwnerProcessID == processID {
			thread, _, openErr := procOpenThread.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
			if thread == 0 {
				_ = syscall.CloseHandle(snapshotHandle)
				return 0, windowsCallError("OpenThread", openErr)
			}
			if closeErr := syscall.CloseHandle(snapshotHandle); closeErr != nil {
				_ = syscall.CloseHandle(syscall.Handle(thread))
				return 0, fmt.Errorf("close thread snapshot: %w", closeErr)
			}
			return syscall.Handle(thread), nil
		}
		ok, _, callErr = procThread32Next.Call(uintptr(snapshotHandle), uintptr(unsafe.Pointer(&entry)))
	}
	closeErr := syscall.CloseHandle(snapshotHandle)
	if callErr == noMoreFiles || callErr == syscall.Errno(0) {
		callErr = fmt.Errorf("primary thread for process %d not found", processID)
	} else {
		callErr = windowsCallError("Thread32Next", callErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close thread snapshot: %w", closeErr)
	}
	return 0, errors.Join(callErr, closeErr)
}

func resumeWindowsThread(thread syscall.Handle) error {
	result, _, callErr := procResumeThread.Call(uintptr(thread))
	if uint32(result) == invalidResumeResult {
		return windowsCallError("ResumeThread", callErr)
	}
	return nil
}

func (owner *windowsTraceCommandOwner) cleanup() error {
	if owner.job == 0 {
		return nil
	}
	job := owner.job
	owner.job = 0
	if err := syscall.CloseHandle(job); err != nil {
		return fmt.Errorf("close traceroute job object: %w", err)
	}
	return nil
}

func windowsCallError(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		err = syscall.EINVAL
	}
	return fmt.Errorf("%s: %w", operation, err)
}
