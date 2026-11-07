package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExposureStartupProcess(t *testing.T) {
	// Run the real startup path in a child test process; localhost:0 stays local
	// but, as a hostname, still exercises the explicit non-literal bind approval.
	if os.Getenv("CHECKNETWORK_STARTUP_TEST_CHILD") == "1" {
		os.Exit(runMain())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExposureStartupProcess$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CHECKNETWORK_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "CHECKNETWORK_STARTUP_TEST_CHILD=1", "CHECKNETWORK_ADDR=localhost:0", "CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes")
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	records := make(chan []map[string]any, 1)
	go func() {
		var entries []map[string]any
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			var record map[string]any
			if json.Unmarshal(scanner.Bytes(), &record) == nil {
				entries = append(entries, record)
				if record["msg"] == "server started" {
					started <- struct{}{}
				}
			}
		}
		records <- entries
	}()
	select {
	case <-started:
		// Stop only this child. Shutdown behavior has separate tests; cancellation
		// avoids racing startup's signal-handler registration or exposing a port.
		cancel()
	case <-ctx.Done():
		t.Error("real server did not start before the test deadline")
	}
	_ = command.Wait() // The child is deliberately terminated after startup.
	var warnings []map[string]any
	for _, record := range <-records {
		if record["level"] == "WARN" {
			warnings = append(warnings, record)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("real startup must emit exactly one exposure warning: %v", warnings)
	}
	if warnings[0]["msg"] != "trusted-local mode serves without authentication on a non-loopback bind" || warnings[0]["address"] != "localhost:0" {
		t.Fatalf("startup warning contract changed: %v", warnings[0])
	}
}
