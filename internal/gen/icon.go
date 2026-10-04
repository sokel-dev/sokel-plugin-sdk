package gen

// Plugin icons (plugin.icon).
//
// Two forms:
//
//	brand:<id>   one of the platform's built-in brand marks (GitLab, Feishu, …), drawn by the platform
//	icon.svg     a file beside the manifest (SVG or PNG), shipped with the plugin's registry entry
//
// An icon is an image a third party gets to put in front of every user of every platform that installs the plugin,
// so the file form is checked where the manifest is loaded from disk: SVG with no scripts, event handlers, embedded
// documents or external references (it is only ever drawn as an image, but the check is what a reviewer can rely
// on); about square; small. A URL is not accepted: a platform would fetch it from users' browsers, which an offline
// or private deployment must not do.

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxIconSVGBytes / MaxIconPNGBytes bound an icon file: a mark, not artwork.
	MaxIconSVGBytes = 32 << 10
	MaxIconPNGBytes = 64 << 10
	// MinIconPNGSize is the smallest PNG side accepted: icons are shown at up to 64px on high-density screens.
	MinIconPNGSize = 128
)

var brandIconRe = regexp.MustCompile(`^brand:[a-z0-9][a-z0-9-]*$`)

// IconIsBrand reports whether icon names a built-in brand mark (brand:<id>).
func IconIsBrand(icon string) bool { return strings.HasPrefix(icon, "brand:") }

// checkIconSyntax validates the plugin.icon value itself (no file access).
func checkIconSyntax(icon string) error {
	if icon == "" {
		return nil
	}
	if IconIsBrand(icon) {
		if !brandIconRe.MatchString(icon) {
			return fmt.Errorf("plugin.icon %q is invalid; a brand mark is brand:<id> with a lowercase id (brand:gitlab)", icon)
		}
		return nil
	}
	if strings.Contains(icon, "://") || strings.HasPrefix(icon, "data:") {
		return fmt.Errorf("plugin.icon %q: a URL is not accepted — ship the file beside the manifest (icon.svg / icon.png) or use brand:<id>", icon)
	}
	clean := path.Clean(icon)
	if path.IsAbs(icon) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(icon, "\\") {
		return fmt.Errorf("plugin.icon %q must be a path inside the plugin's directory", icon)
	}
	switch strings.ToLower(path.Ext(icon)) {
	case ".svg", ".png":
		return nil
	}
	return fmt.Errorf("plugin.icon %q must be an .svg or .png file (or brand:<id>)", icon)
}

// iconFile returns the icon's path on disk when the manifest was loaded from a file and names one.
func (m *Manifest) iconFile() (string, bool) {
	icon := strings.TrimSpace(m.Plugin.Icon)
	if icon == "" || IconIsBrand(icon) || m.path == "" {
		return "", false
	}
	return filepath.Join(filepath.Dir(m.path), filepath.FromSlash(icon)), true
}

// ValidateIconFile checks an icon file: SVG or PNG, small, about square, and for SVG nothing that runs or reaches
// outside the file.
func ValidateIconFile(p string) error {
	raw, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("icon: %w", err)
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".svg":
		if len(raw) > MaxIconSVGBytes {
			return fmt.Errorf("icon %s is %d bytes; an SVG icon may be at most %d", filepath.Base(p), len(raw), MaxIconSVGBytes)
		}
		return checkSVG(raw)
	case ".png":
		if len(raw) > MaxIconPNGBytes {
			return fmt.Errorf("icon %s is %d bytes; a PNG icon may be at most %d", filepath.Base(p), len(raw), MaxIconPNGBytes)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("icon %s is not a readable PNG: %w", filepath.Base(p), err)
		}
		if cfg.Width < MinIconPNGSize || cfg.Height < MinIconPNGSize {
			return fmt.Errorf("icon %s is %dx%d; a PNG icon must be at least %dx%d", filepath.Base(p), cfg.Width, cfg.Height, MinIconPNGSize, MinIconPNGSize)
		}
		return checkSquare(float64(cfg.Width), float64(cfg.Height), filepath.Base(p))
	}
	return fmt.Errorf("icon %s must be .svg or .png", filepath.Base(p))
}

func checkSquare(w, h float64, name string) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("icon %s has no size", name)
	}
	if r := w / h; r < 0.8 || r > 1.25 {
		return fmt.Errorf("icon %s is %gx%g; it must be about square (it is drawn in a square tile)", name, w, h)
	}
	return nil
}

// Elements that run code, embed another document, or animate attributes into something else.
var svgForbidden = map[string]bool{"script": true, "foreignobject": true, "iframe": true, "embed": true, "object": true,
	"audio": true, "video": true, "set": true, "animate": true, "handler": true, "listener": true}

var cssExternalRe = regexp.MustCompile(`(?i)@import|url\(\s*['"]?\s*[^'"\s#)]`)

func checkSVG(raw []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	var root *xml.StartElement
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("icon is not well-formed SVG: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			// DOCTYPE carries entity declarations: the classic way to smuggle content in.
			return fmt.Errorf("icon SVG must not contain a DOCTYPE or entity declarations")
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if root == nil {
				if name != "svg" {
					return fmt.Errorf("icon root element is <%s>, not <svg>", t.Name.Local)
				}
				el := t.Copy()
				root = &el
			}
			if svgForbidden[name] {
				return fmt.Errorf("icon SVG contains <%s>; scripts, embedded documents and animations are not allowed", t.Name.Local)
			}
			for _, a := range t.Attr {
				an := strings.ToLower(a.Name.Local)
				if strings.HasPrefix(an, "on") {
					return fmt.Errorf("icon SVG has an event handler attribute (%s)", a.Name.Local)
				}
				if an == "href" && !strings.HasPrefix(strings.TrimSpace(a.Value), "#") {
					return fmt.Errorf("icon SVG links outside itself (href=%q); only #fragment references are allowed", a.Value)
				}
				if an == "style" && cssExternalRe.MatchString(a.Value) {
					return fmt.Errorf("icon SVG style references something outside the file")
				}
			}
		case xml.CharData:
			if cssExternalRe.Match(t) {
				return fmt.Errorf("icon SVG style references something outside the file")
			}
		}
	}
	if root == nil {
		return fmt.Errorf("icon has no <svg> element")
	}
	return checkSVGShape(root)
}

func checkSVGShape(root *xml.StartElement) error {
	attr := map[string]string{}
	for _, a := range root.Attr {
		attr[strings.ToLower(a.Name.Local)] = a.Value
	}
	if vb := strings.Fields(strings.ReplaceAll(attr["viewbox"], ",", " ")); len(vb) == 4 {
		w, _ := strconv.ParseFloat(vb[2], 64)
		h, _ := strconv.ParseFloat(vb[3], 64)
		return checkSquare(w, h, "SVG")
	}
	w, _ := strconv.ParseFloat(strings.TrimSuffix(attr["width"], "px"), 64)
	h, _ := strconv.ParseFloat(strings.TrimSuffix(attr["height"], "px"), 64)
	if w > 0 && h > 0 {
		return checkSquare(w, h, "SVG")
	}
	return fmt.Errorf("icon SVG needs a viewBox (or width and height) so it can be drawn at any size")
}
