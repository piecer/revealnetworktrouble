//go:build !windows

package diagnostic

import (
	"reflect"
	"testing"
	"time"
)

func TestUnixTraceCommandSpecRetainsTracerouteArguments(t *testing.T) {
	name, args := traceCommandSpec(1750*time.Millisecond, "example.test")
	if name != "traceroute" {
		t.Fatalf("name = %q, want traceroute", name)
	}
	want := []string{"-n", "-q", "1", "-w", "2", "-m", "30", "example.test"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
