//go:build windows

package diagnostic

import (
	"reflect"
	"testing"
	"time"
)

func TestWindowsTraceCommandSpecUsesBoundedAttemptTimeoutMilliseconds(t *testing.T) {
	for _, tt := range []struct {
		name    string
		timeout time.Duration
		wantMS  string
	}{
		{name: "current timeout", timeout: 1750 * time.Millisecond, wantMS: "1750"},
		{name: "minimum", timeout: 0, wantMS: "1"},
		{name: "maximum", timeout: MaxTimeout + time.Second, wantMS: "30000"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			name, args := traceCommandSpec(tt.timeout, "203.0.113.8")
			if name != "tracert.exe" {
				t.Fatalf("name = %q, want tracert.exe", name)
			}
			want := []string{"-d", "-w", tt.wantMS, "-h", "30", "203.0.113.8"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("args = %#v, want %#v", args, want)
			}
		})
	}
}
