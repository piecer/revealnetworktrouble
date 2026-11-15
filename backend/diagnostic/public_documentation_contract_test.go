package diagnostic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep current entry points discoverable without freezing prose or test totals.
func TestPublicDocumentationEntryPointsAndLinks(t *testing.T) {
	root := documentationRepositoryRoot(t)
	pages := map[string][]string{
		"README.md":            {"README.en.md", "CONTRIBUTING.md", "SECURITY.md", "ROADMAP.md", "docs/API.md", "docs/OPERATIONS.md", "docs/ARCHITECTURE.md", "docs/TESTING.md", "docs/archive/IMPLEMENTATION-HISTORY.md"},
		"README.en.md":         {"README.md", "CONTRIBUTING.md", "SECURITY.md", "ROADMAP.md", "docs/API.md", "docs/OPERATIONS.md", "docs/TESTING.md"},
		"CONTRIBUTING.md":      {"README.md", "SECURITY.md", "ROADMAP.md", "docs/TESTING.md"},
		"docs/ARCHITECTURE.md": {"API.md", "OPERATIONS.md", "TESTING.md", "archive/IMPLEMENTATION-HISTORY.md"},
	}
	for page, targets := range pages {
		t.Run(page, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range targets {
				if !strings.Contains(string(data), "("+target+")") {
					t.Errorf("missing entry-point link %q", target)
				}
			}
		})
	}
}
