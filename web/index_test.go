package web

import (
	"os"
	"strings"
	"testing"
)

func TestUpdateCheckUsesUpstreamRepository(t *testing.T) {
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, "raw.githubusercontent.com/Quorinex/Kiro-Go/main/version.json") {
		t.Fatalf("update check should query upstream Quorinex repository")
	}
	if strings.Contains(html, "raw.githubusercontent.com/XuYang8026/Kiro-Go/main/version.json") {
		t.Fatalf("update check should not query fork repository for latest upstream version")
	}
}

func TestIdentityOverrideSettingsUIExists(t *testing.T) {
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(data)

	required := []string{
		"id=\"identityOverrideEnabled\"",
		"id=\"identityOverrideResponse\"",
		"/admin/api/identity",
		"saveIdentityOverrideConfig",
	}
	for _, needle := range required {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected identity settings UI to contain %q", needle)
		}
	}
}
