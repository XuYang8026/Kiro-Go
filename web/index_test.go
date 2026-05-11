package web

import (
	"os"
	"strings"
	"testing"
)

func TestUpdateCheckUsesForkRepository(t *testing.T) {
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(data)

	if strings.Contains(html, "raw.githubusercontent.com/Quorinex/Kiro-Go/main/version.json") {
		t.Fatalf("update check should not query upstream Quorinex repository")
	}
	if !strings.Contains(html, "raw.githubusercontent.com/XuYang8026/Kiro-Go/main/version.json") {
		t.Fatalf("update check should query XuYang8026 fork repository")
	}
}
