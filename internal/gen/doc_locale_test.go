package gen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUsageMarkdownTranslationSurvivesManifestExport(t *testing.T) {
	dir := t.TempDir()
	doc := "# Source guide\n\nUse `format_id`.\n"
	translated := "# Translated guide\n\nUse `format_id`.\n"
	if err := os.Mkdir(filepath.Join(dir, "locales"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{doc: translated})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"manifest.yml": []byte("plugin: {name: demo, doc: README.md}\noperations: [{id: ping, label: Ping}]\n"),
		"README.md":    []byte(doc), "locales/en.json": raw,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := LoadManifest(filepath.Join(dir, "manifest.yml"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ExportManifestJSON(m, doc)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Doc     string                       `json:"doc"`
		Locales map[string]map[string]string `json:"locales"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Doc != doc || out.Locales["en"][doc] != translated {
		t.Fatalf("usage translation missing from export: %s", b)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(doc+"Changed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(filepath.Join(dir, "manifest.yml")); err == nil {
		t.Fatal("stale usage translation must fail after source changes")
	}
}
