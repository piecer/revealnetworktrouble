//go:build linux

package diagnostic

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProcessPIDExistsDistinguishesLiveAndUnreapedProcesses(t *testing.T) {
	if !processPIDExists(os.Getpid()) {
		t.Fatal("the test process must be considered alive")
	}

	// Leave our own child unreaped so the test does not depend on PID 1.
	command := exec.Command("sh", "-c", "exit 0")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Wait() })
	pid := command.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(stat), ") Z ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not enter the unreaped zombie state")
		}
		time.Sleep(time.Millisecond)
	}
	if processPIDExists(pid) {
		t.Fatal("an exited, unreaped child is not a surviving process")
	}
}
