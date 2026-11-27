package diagnostic

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDocumentationContractRequiredFilesAndLocalLinks(t *testing.T) {
	root := documentationRepositoryRoot(t)
	for _, path := range []string{
		"README.md", "README.en.md", "CONTRIBUTING.md", "SECURITY.md", "CHANGELOG.md", "LICENSE", "ROADMAP.md",
		"docs/API.md", "docs/ARCHITECTURE.md", "docs/OPERATIONS.md", "docs/TESTING.md", "docs/CI.md", "android/README.md",
		"docs/archive/README.md", "docs/archive/SPEC.md", "docs/archive/PLAN.md",
		"docs/archive/IMPLEMENTATION-HISTORY.md", "docs/archive/IMPROVEMENT-PLAN-v1.1.md",
		"docs/archive/PHASE1-VERIFICATION.md", "docs/archive/PHASE2-VERIFICATION.md",
		"docs/archive/REVIEW_2026-09-01.md", "docs/archive/README-TECHNICAL-NOTES.md",
	} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil || !info.Mode().IsRegular() {
			t.Errorf("required document %s is missing or not a regular file: %v", path, err)
		}
	}

	// Check inline document/image links, including archived documents. Fragment
	// anchors and reference-style Markdown links are not validated by this test.
	paths, err := filepath.Glob(filepath.Join(root, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) > 6 {
		t.Errorf("root has %d Markdown documents; keep entry points here and records in docs/archive", len(paths))
	}
	paths = append(paths, filepath.Join(root, "android", "README.md"))
	if err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".md" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	links := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			continue
		}
		for _, match := range links.FindAllStringSubmatch(string(data), -1) {
			target := strings.SplitN(match[1], "#", 2)[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(target))); err != nil {
				t.Errorf("%s has a broken local link %q: %v", path, target, err)
			}
		}
	}
}

// Live documents must not assert test-inventory cardinality. Those numbers drift
// with every added test and a document cannot verify its own claim, so past
// readings live only in docs/archive and current cardinality is observed by
// running the gate. Contract-pinned fixture cardinalities are enforced by tests
// rather than asserted in prose.
func TestLiveDocumentationDoesNotAssertTestCounts(t *testing.T) {
	root := documentationRepositoryRoot(t)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`[0-9]+[- ]tests?\b`),
		regexp.MustCompile(`[0-9]+ \+ [0-9]+ = [0-9]+`),
		regexp.MustCompile(`[0-9][0-9,]*\s*(개\s*)?(tests?|테스트|cases?|케이스)`),
		regexp.MustCompile(`(테스트|tests?)\s*(수|개수)?\s*[0-9]+`),
		regexp.MustCompile(`[0-9]+\s*\+\s*(\S+\s+)?[0-9]+\s*=\s*[0-9]+`),
	}
	var live []string
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
			live = append(live, filepath.Join(root, entry.Name()))
		}
	}
	docs := filepath.Join(root, "docs")
	docEntries, err := os.ReadDir(docs)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range docEntries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
			live = append(live, filepath.Join(docs, entry.Name()))
		}
	}
	live = append(live, filepath.Join(root, "android", "README.md"))
	if len(live) == 0 {
		t.Fatal("no live documents were inspected")
	}
	for _, path := range live {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, pattern := range patterns {
				if match := pattern.FindString(line); match != "" {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d asserts a test count (%q): %s", rel, i+1, match, line)
				}
			}
		}
	}
}

type documentedAPIError struct {
	Key        string `json:"key"`
	Status     int    `json:"status"`
	Code       string `json:"code"`
	Retryable  bool   `json:"retryable"`
	RetryAfter bool   `json:"retry_after"`
	Message    string `json:"message"`
}

func TestDocumentationContractAPIErrorTable(t *testing.T) {
	root := documentationRepositoryRoot(t)
	fixture, err := os.ReadFile(filepath.Join(root, "testdata", "api-error-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Errors []documentedAPIError `json:"errors"`
	}
	if err := json.Unmarshal(fixture, &contract); err != nil || len(contract.Errors) == 0 {
		t.Fatalf("invalid API error contract: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "API.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented, err := parseDocumentedAPIErrors(string(data))
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]documentedAPIError, len(contract.Errors))
	for _, row := range contract.Errors {
		want[row.Key] = row
	}
	if !reflect.DeepEqual(documented, want) {
		t.Errorf("API documentation table differs from producer fixture\n got: %#v\nwant: %#v", documented, want)
	}
}

func parseDocumentedAPIErrors(text string) (map[string]documentedAPIError, error) {
	const header = "| registry key | HTTP | wire code |"
	if strings.Count(text, header) != 1 {
		return nil, fmt.Errorf("expected one API error table")
	}
	rows := make(map[string]documentedAPIError)
	inTable := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, header) {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		if strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 6 {
			return nil, fmt.Errorf("invalid API error table row: %q", line)
		}
		for i := range cells {
			cells[i] = strings.Trim(strings.TrimSpace(cells[i]), "`")
		}
		status, err := strconv.Atoi(cells[1])
		if err != nil || (cells[3] != "yes" && cells[3] != "no") || (cells[4] != "yes" && cells[4] != "no") {
			return nil, fmt.Errorf("invalid HTTP status or retry flags in API row: %q", line)
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("duplicate API error key %q", cells[0])
		}
		code := cells[2]
		if code == "same" {
			code = cells[0]
		}
		rows[cells[0]] = documentedAPIError{cells[0], status, code, cells[3] == "yes", cells[4] == "yes", cells[5]}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("API error table is empty")
	}
	return rows, nil
}

func documentationRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate documentation contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
