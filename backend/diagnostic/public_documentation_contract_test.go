package diagnostic

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Living entry points must stand on their own; frozen stage claims belong in history.
func TestPublicDocumentationEntryPointsAndLinks(t *testing.T) {
	root := documentationRepositoryRoot(t)
	pages := map[string][]string{
		"README.md":                      {"README.en.md", "CONTRIBUTING.md", "SECURITY.md", "docs/IMPLEMENTATION-HISTORY.md", "http://localhost:8090", "서버 측", "trusted-local", "public"},
		"README.en.md":                   {"Server-side measurements", "http://localhost:8090", "CONTRIBUTING.md", "SECURITY.md", "trusted-local", "public"},
		"CONTRIBUTING.md":                {"make ci-quick", "make ci", "make ci-security", "make ci-policy", "SECURITY.md"},
		"SECURITY.md":                    {"trusted-local", "public", "CHECKNETWORK_EXPOSE_TRUSTED_LOCAL", "비공개", "SSRF"},
		"CHANGELOG.md":                   {"[Unreleased]", "734885a", "0.1.0"},
		"docs/ARCHITECTURE.md":           {"## 1. 시스템 구성", "## 2. 진단 데이터 흐름", "## 3. Checker", "## 4. 보안 신뢰 경계", "## 5. 동시성", "## 6. Web/Android", "## 7. 확장", "## 8. 설계상 한계", "backend/api", "backend/diagnostic", "Analyze", "RemoteAddr", "IMPLEMENTATION-HISTORY.md"},
		"docs/IMPLEMENTATION-HISTORY.md": {"Historical snapshot", "734885a", "298 tests", "Web 620"},
		"LICENSE":                        {"Apache License", "Version 2.0, January 2004", "END OF TERMS AND CONDITIONS"},
	}
	links := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	for page, claims := range pages {
		t.Run(page, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
			if err != nil {
				t.Fatalf("required public document missing: %v", err)
			}
			text := string(data)
			for _, claim := range claims {
				if !strings.Contains(text, claim) {
					t.Errorf("missing entry-point contract %q", claim)
				}
			}
			for _, match := range links.FindAllStringSubmatch(text, -1) {
				target := strings.SplitN(match[1], "#", 2)[0]
				if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.Dir(page), filepath.FromSlash(target))); err != nil {
					t.Errorf("broken local document/image link %q: %v", target, err)
				}
			}
		})
	}
}
