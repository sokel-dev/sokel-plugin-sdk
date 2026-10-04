package gen

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIconSyntax(t *testing.T) {
	cases := []struct {
		icon, wantErr string
	}{
		{"", ""},
		{"brand:gitlab", ""},
		{"brand:alibaba-cloud", ""},
		{"brand:GitLab", "lowercase"},
		{"brand:", "lowercase"},
		{"icon.svg", ""},
		{"assets/icon.png", ""},
		{"https://example.com/icon.svg", "URL"},
		{"data:image/svg+xml;base64,AAAA", "URL"},
		{"../icon.svg", "inside the plugin's directory"},
		{"/etc/icon.svg", "inside the plugin's directory"},
		{"icon.gif", ".svg or .png"},
	}
	for _, c := range cases {
		err := checkIconSyntax(c.icon)
		if c.wantErr == "" && err != nil {
			t.Errorf("%q: unexpected %v", c.icon, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%q: want an error containing %q, got %v", c.icon, c.wantErr, err)
		}
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestValidateIconFile(t *testing.T) {
	const ok = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><defs><linearGradient id="g"/></defs><path fill="url(#g)" d="M0 0h24v24H0z"/><use href="#g"/></svg>`
	cases := []struct {
		name    string
		file    string
		body    []byte
		wantErr string
	}{
		{"plain svg", "i.svg", []byte(ok), ""},
		{"script", "i.svg", []byte(`<svg viewBox="0 0 24 24"><script>alert(1)</script></svg>`), "<script>"},
		{"event handler", "i.svg", []byte(`<svg viewBox="0 0 24 24" onload="x()"></svg>`), "event handler"},
		{"external href", "i.svg", []byte(`<svg viewBox="0 0 24 24" xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="https://evil/x.png"/></svg>`), "links outside"},
		{"style url", "i.svg", []byte(`<svg viewBox="0 0 24 24"><style>path{fill:url(https://evil/x)}</style></svg>`), "outside the file"},
		{"style import", "i.svg", []byte(`<svg viewBox="0 0 24 24"><style>@import "x.css";</style></svg>`), "outside the file"},
		{"foreignObject", "i.svg", []byte(`<svg viewBox="0 0 24 24"><foreignObject/></svg>`), "foreignObject"},
		{"doctype", "i.svg", []byte(`<!DOCTYPE svg [<!ENTITY x "y">]><svg viewBox="0 0 24 24"></svg>`), "DOCTYPE"},
		{"not svg root", "i.svg", []byte(`<html></html>`), "not <svg>"},
		{"wide", "i.svg", []byte(`<svg viewBox="0 0 48 24"></svg>`), "about square"},
		{"no size", "i.svg", []byte(`<svg></svg>`), "viewBox"},
		{"too big svg", "i.svg", append([]byte(`<svg viewBox="0 0 24 24"><!--`), append(bytes.Repeat([]byte("x"), MaxIconSVGBytes), []byte(`--></svg>`)...)...), "at most"},
		{"png ok", "i.png", pngBytes(t, 256, 256), ""},
		{"png too small", "i.png", pngBytes(t, 64, 64), "at least"},
		{"png wide", "i.png", pngBytes(t, 512, 256), "about square"},
		{"png garbage", "i.png", []byte("nope"), "not a readable PNG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), c.file)
			if err := os.WriteFile(p, c.body, 0o644); err != nil {
				t.Fatal(err)
			}
			err := ValidateIconFile(p)
			if c.wantErr == "" && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("want an error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

// Loading a manifest from disk checks the icon file it names, and the index entry carries the icon.
func TestManifestIcon(t *testing.T) {
	write := func(t *testing.T, icon string, files map[string]string) string {
		dir := t.TempDir()
		m := "plugin: {name: foo, org: acme, label: Foo, version: 1.0.0, icon: " + icon + "}\noperations:\n  - {id: ping, label: Ping, inputs: [], outputs: []}\n"
		files["manifest.yml"] = m
		for f, body := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "manifest.yml")
	}
	m, err := LoadManifest(write(t, "icon.svg", map[string]string{"icon.svg": `<svg viewBox="0 0 24 24"><path d="M0 0h1"/></svg>`}))
	if err != nil {
		t.Fatalf("a valid icon file: %v", err)
	}
	e, err := BuildIndexEntry(m, "")
	if err != nil || e.Icon != "icon.svg" {
		t.Fatalf("index entry icon: %v %+v", err, e)
	}
	if _, err := LoadManifest(write(t, "icon.svg", map[string]string{})); err == nil || !strings.Contains(err.Error(), "plugin.icon") {
		t.Errorf("a missing icon file must fail the load: %v", err)
	}
	if _, err := LoadManifest(write(t, "icon.svg", map[string]string{"icon.svg": `<svg viewBox="0 0 24 24" onload="x()"/>`})); err == nil {
		t.Error("an unsafe icon must fail the load")
	}
	if m, err := LoadManifest(write(t, "brand:gitlab", map[string]string{})); err != nil || m.Plugin.Icon != "brand:gitlab" {
		t.Errorf("a brand icon needs no file: %v", err)
	}
	// Parsing text without a directory checks only the value.
	if _, err := ParseManifest([]byte("plugin: {name: foo, icon: icon.svg}\noperations:\n  - {id: ping, label: Ping, inputs: [], outputs: []}\n"), false); err != nil {
		t.Errorf("parsing text cannot see files and must not demand them: %v", err)
	}
}
