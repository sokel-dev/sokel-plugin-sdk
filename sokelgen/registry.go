package sokelgen

// A registry is **a directory of manifests**, nothing more. Building an index means walking it,
// validating each entry, and writing one file the platform can fetch.
//
// It lives in the SDK because that is where manifest semantics live: an index built by anything
// else would have to re-implement parsing, validation and the org/name identity rule, and the
// second implementation is the one that drifts.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginsSub is the directory holding the entries, laid out as plugins/<org>/<name>/manifest.yml.
// The path **is** the identity claim: which directory a submission touches is what a reviewer
// checks, and under GitOps it is also what CODEOWNERS can gate.
const PluginsSub = "plugins"

// IndexEntry is one row of the index: enough to render a store card and to decide installability,
// without downloading every manifest. The full contract is fetched at install time.
type IndexEntry struct {
	Ref     string `json:"ref"` // <org>/<name> — the global identity
	Org     string `json:"org"`
	Name    string `json:"name"`
	Label   string `json:"label,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Version string `json:"version,omitempty"`
	// Capabilities is what the plugin implements, so the store can filter **before** installing —
	// the whole point of putting metadata up front.
	Capabilities []string `json:"capabilities,omitempty"`
	// Operations / Events are counts, not contents: a card shows "12 operations", and carrying the
	// full contract here would make the index grow with every plugin's every field.
	Operations int `json:"operations"`
	Events     int `json:"events"`
	// Langs are the translations the plugin ships. The platform falls back to the source string for
	// anything missing, so this is for display ("available in…"), not for gating.
	Langs []string `json:"langs,omitempty"`
	// Deploy lists the artifact kinds this plugin ships (container / binary / pip / npm).
	//
	// It answers "can I run a replica myself", **not** "do I have to". Whether the platform can
	// serve this plugin in-process is a property of the platform binary, not of the plugin, so the
	// index deliberately does not carry it — the store combines this field with what the local
	// platform knows it has compiled in, and renders both facts on the card.
	Deploy []string `json:"deploy,omitempty"`
	// Manifest is the path to the full manifest, relative to the index.
	Manifest string `json:"manifest"`
}

// Index is what the platform fetches.
type Index struct {
	// Version lets a reader reject a format it does not understand instead of guessing at fields.
	Version int          `json:"version"`
	Plugins []IndexEntry `json:"plugins"`
}

// IndexVersion is the current index format.
const IndexVersion = 1

// BuildIndex walks <root>/plugins/<org>/<name>/ and returns the index.
//
// Every manifest is **loaded through the normal path**, so an entry that would not pass
// `sokel-gen check` cannot enter the index. A registry that serves a broken manifest turns one
// author's mistake into every installer's problem.
func BuildIndex(root string) (*Index, error) {
	base := filepath.Join(root, PluginsSub)
	orgs, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", base, err)
	}
	idx := &Index{Version: IndexVersion, Plugins: []IndexEntry{}}
	var problems []string
	for _, org := range orgs {
		if !org.IsDir() {
			continue
		}
		names, nerr := os.ReadDir(filepath.Join(base, org.Name()))
		if nerr != nil {
			return nil, nerr
		}
		for _, name := range names {
			if !name.IsDir() {
				continue
			}
			dir := filepath.Join(base, org.Name(), name.Name())
			entry, eerr := indexOne(root, dir, org.Name(), name.Name())
			if eerr != nil {
				problems = append(problems, eerr.Error())
				continue
			}
			idx.Plugins = append(idx.Plugins, *entry)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%d entries did not build:\n  - %s", len(problems), strings.Join(problems, "\n  - "))
	}
	sort.Slice(idx.Plugins, func(i, j int) bool { return idx.Plugins[i].Ref < idx.Plugins[j].Ref })
	return idx, nil
}

func indexOne(root, dir, org, name string) (*IndexEntry, error) {
	path, ferr := FindManifest(dir)
	if ferr != nil {
		return nil, fmt.Errorf("%s/%s: %w", org, name, ferr)
	}
	if path == "" {
		return nil, fmt.Errorf("%s/%s: no manifest in %s", org, name, dir)
	}
	m, lerr := LoadManifest(path)
	if lerr != nil {
		return nil, fmt.Errorf("%s/%s: %w", org, name, lerr)
	}
	// The directory is the identity; the manifest only restates it. Disagreement is not a detail to
	// paper over — whichever one a reviewer read, the other is what would actually get installed.
	if m.Plugin.Org != "" && m.Plugin.Org != org {
		return nil, fmt.Errorf("%s/%s: manifest says org %q but it sits under %q", org, name, m.Plugin.Org, org)
	}
	if m.Plugin.Name != "" && m.Plugin.Name != name {
		return nil, fmt.Errorf("%s/%s: manifest says name %q but the directory is %q", org, name, m.Plugin.Name, name)
	}
	caps := make([]string, 0, len(m.Implements))
	for _, c := range m.Implements {
		caps = append(caps, c.Capability)
	}
	langs := sortedKeys(m.Locales)
	var deploy []string
	if m.Deployment != nil {
		for _, t := range m.Deployment.Targets {
			deploy = append(deploy, t.Kind)
		}
	}
	rel, rerr := filepath.Rel(root, path)
	if rerr != nil {
		rel = path
	}
	return &IndexEntry{
		Ref: org + "/" + name, Org: org, Name: name,
		Label: m.Plugin.Label, Desc: m.Plugin.Desc, Version: m.Plugin.Version,
		Capabilities: caps,
		Operations:   len(m.AllOperations()), Events: len(m.Events),
		Langs:    langs,
		Deploy:   deploy,
		Manifest: filepath.ToSlash(rel),
	}, nil
}

// RenderIndex writes the index as indented JSON: it is reviewed in pull requests, and a one-line
// file makes every change look like the whole thing changed.
func RenderIndex(idx *Index) ([]byte, error) {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
