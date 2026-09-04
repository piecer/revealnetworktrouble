//go:build !windows

package diagnostic

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTracerouteExecutableAvailableUsesExactPlatformCommand(t *testing.T) {
	if name := traceExecutableName(); name != "traceroute" {
		t.Fatalf("Unix executable name=%q", name)
	}
	t.Setenv("PATH", t.TempDir())
	if TracerouteExecutableAvailable() {
		t.Fatal("reported traceroute available with an empty PATH")
	}

	directory := t.TempDir()
	executable := filepath.Join(directory, "traceroute")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	if TracerouteExecutableAvailable() {
		t.Fatal("accepted an executable that did not produce a functional loopback trace")
	}

	if err := os.Rename(executable, filepath.Join(directory, "tracert.exe")); err != nil {
		t.Fatal(err)
	}
	if TracerouteExecutableAvailable() {
		t.Fatal("accepted Windows executable name on Unix")
	}
}

func TestProbeTracerouteCapabilitySelectsCompatibleGrammarAndExecutionUsesIt(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "traceroute")
	script := `#!/bin/sh
if [ "$1" = "-n" ]; then
  exit 64
fi
if [ "$1" != "-q" ] || [ "$2" != "1" ] || [ "$3" != "-w" ] || [ "$4" != "2" ] || [ "$5" != "-m" ] || [ "$6" != "30" ] || [ "$7" != "127.0.0.1" ]; then
  exit 65
fi
printf 'traceroute to 127.0.0.1 (127.0.0.1), 30 hops max\n1  127.0.0.1  0.01 ms\n'
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	capability, err := ProbeTracerouteCapability()
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	name, args := capability.commandSpec(1750*time.Millisecond, "example.test")
	wantArgs := []string{"-q", "1", "-w", "2", "-m", "30", "example.test"}
	if name != executable || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("selected command = %q %#v, want %q %#v", name, args, executable, wantArgs)
	}

	var executedName string
	var executedArgs []string
	checker := newTracerouteCheckerWithCommand(capability, func(_ context.Context, name string, args ...string) ([]byte, error) {
		executedName = name
		executedArgs = append([]string(nil), args...)
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  0.01 ms\n"), nil
	}, nil, nil)
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
	if result.Status != StatusHealthy {
		t.Fatalf("result = %+v", result)
	}
	if executedName != name || !reflect.DeepEqual(executedArgs, args) {
		t.Fatalf("executed command = %q %#v, probed command = %q %#v", executedName, executedArgs, name, args)
	}
}

func TestProductionCheckerKeepsExactProbedExecutableAfterPATHChanges(t *testing.T) {
	startupDirectory := t.TempDir()
	startupExecutable := filepath.Join(startupDirectory, "traceroute")
	startupCalls := filepath.Join(startupDirectory, "calls")
	startupScript := "#!/bin/sh\nprintf called >> \"" + startupCalls + "\"\ndestination=127.0.0.1\nfor argument do destination=$argument; done\nprintf 'traceroute to %s (%s), 30 hops max\\n1  %s  0.01 ms\\n' \"$destination\" \"$destination\" \"$destination\"\n"
	if err := os.WriteFile(startupExecutable, []byte(startupScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", startupDirectory)
	capability, err := ProbeTracerouteCapability()
	if err != nil {
		t.Fatal(err)
	}

	lateDirectory := t.TempDir()
	lateCalls := filepath.Join(lateDirectory, "calls")
	if err := os.WriteFile(filepath.Join(lateDirectory, "traceroute"), []byte("#!/bin/sh\nprintf called >> \""+lateCalls+"\"\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", lateDirectory)

	checker := NewTracerouteChecker(capability, nil, nil)
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "127.0.0.1", Attempts: 1})
	if result.Status != StatusHealthy {
		t.Fatalf("exact startup capability result = %+v", result)
	}
	startupOutput, err := os.ReadFile(startupCalls)
	if err != nil || strings.Count(string(startupOutput), "called") < 2 {
		t.Fatalf("startup executable calls=%q error=%v", startupOutput, err)
	}
	if _, err := os.Stat(lateCalls); !os.IsNotExist(err) {
		t.Fatalf("late PATH executable was invoked: %v", err)
	}
}

func TestProbeTracerouteCapabilityFailsWhenEveryGrammarIsRejected(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "traceroute")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 64\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	capability, err := ProbeTracerouteCapability()
	if capability != nil || !errors.Is(err, ErrTracerouteUnavailable) {
		t.Fatalf("capability=%v error=%v", capability, err)
	}
	if TracerouteExecutableAvailable() {
		t.Fatal("readiness accepted an executable whose grammars all failed")
	}
}

func TestProbeTracerouteCapabilityBoundsTotalTimeout(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "traceroute")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n/bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	started := time.Now()
	capability, err := ProbeTracerouteCapability()
	elapsed := time.Since(started)
	if capability != nil || !errors.Is(err, ErrTracerouteUnavailable) {
		t.Fatalf("capability=%v error=%v", capability, err)
	}
	if elapsed < tracerouteProbeTimeout/2 || elapsed > tracerouteProbeTimeout+time.Second {
		t.Fatalf("probe elapsed=%v, timeout=%v", elapsed, tracerouteProbeTimeout)
	}
}

func TestProbeTracerouteCapabilityRejectsOversizeOutput(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "traceroute")
	script := "#!/bin/sh\n/bin/dd if=/dev/zero bs=" + strconv.Itoa(tracerouteProbeOutputBytes+1) + " count=1 2>/dev/null\nprintf 'traceroute to 127.0.0.1 (127.0.0.1), 30 hops max\\n1  127.0.0.1  0.01 ms\\n'\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	started := time.Now()
	capability, err := ProbeTracerouteCapability()
	if capability != nil || !errors.Is(err, ErrTracerouteUnavailable) {
		t.Fatalf("capability=%v error=%v", capability, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("oversize probe was not terminated promptly: %v", elapsed)
	}
}

func TestLiveGNUInetutilsTracerouteLoopbackIsReadyAndSucceeds(t *testing.T) {
	executable, err := exec.LookPath("traceroute")
	if err != nil {
		t.Skip("traceroute is not installed")
	}
	versionOutput, err := exec.Command(executable, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOutput), "GNU inetutils") {
		t.Skip("installed traceroute is not GNU inetutils")
	}

	capability, err := ProbeTracerouteCapability()
	if err != nil {
		t.Fatalf("GNU inetutils readiness probe failed: %v", err)
	}
	name, args := capability.commandSpec(2*time.Second, tracerouteProbeAddress)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := runTraceCommand(ctx, name, args...)
	if err != nil {
		t.Fatalf("GNU inetutils selected command failed: %v\n%s", err, output)
	}
	topology, err := parseTraceroute(string(output), tracerouteProbeAddress)
	if err != nil || !topology.Reached {
		t.Fatalf("GNU inetutils loopback topology=%+v error=%v output=%q", topology, err, output)
	}
}
