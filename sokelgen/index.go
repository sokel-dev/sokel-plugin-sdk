package sokelgen

// Distribution-index helpers, shared by every builder of an index.json.
//
// A registry's index has exactly one producer today (the repo's build-index CLI) and is about to
// gain a second (the platform's registry-tools plugin, which builds an index from reviewed
// submissions inside a workflow). Deriving an entry's fields in two places is how the two drift
// apart — the shared part lives here, next to the manifest parser both already use.
//
// The path-shaped fields (Manifest, Files) stay with the caller: a filesystem builder derives them
// from what is actually on disk, an in-memory builder from convention. Everything derivable from
// the manifest alone is filled by BuildIndexEntry.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// IndexVersion is the index format this toolchain produces. Consumers reject a version they do not
// recognise wholesale — "best-effort parsing" of an unknown format loses half the catalog silently.
const IndexVersion = 2

// IndexEntry is one plugin entry of a distribution index (index.json).
type IndexEntry struct {
	Ref          string   `json:"ref"`
	Org          string   `json:"org"`
	Name         string   `json:"name"`
	Label        string   `json:"label,omitempty"`
	Desc         string   `json:"desc,omitempty"`
	Version      string   `json:"version,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Operations   int      `json:"operations"`
	Events       int      `json:"events"`
	Langs        []string `json:"langs,omitempty"`
	Deploy       []string `json:"deploy,omitempty"`
	Manifest     string   `json:"manifest"`
	Files        []string `json:"files,omitempty"`
}

// distVersionRe: v?MAJOR.MINOR.PATCH(-prerelease)?. In a manifest the version is optional (a plugin
// under development has none yet); at the distribution gate it is REQUIRED — the update badge,
// advisory matching and range comparison all live off it, and an unversioned catalog entry keeps
// every one of them silent forever.
var distVersionRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// BuildIndexEntry derives the manifest-owned fields of an index entry: identity, stats
// (operation/event counts, capabilities, locale langs, deploy target kinds) — and runs the two
// distribution-gate checks that must not wait for a platform to boot: the version is present and
// well-formed, and the contract actually derives (ExportManifestJSON succeeds).
//
// Manifest and Files are left empty — they are path-shaped and belong to the caller.
func BuildIndexEntry(m *Manifest, doc string) (*IndexEntry, error) {
	org, name := strings.TrimSpace(m.Plugin.Org), strings.TrimSpace(m.Plugin.Name)
	if org == "" || name == "" {
		return nil, fmt.Errorf("plugin.org and plugin.name are required for distribution")
	}
	if !distVersionRe.MatchString(m.Plugin.Version) {
		return nil, fmt.Errorf("plugin.version %q is invalid — required at the distribution gate, format v?MAJOR.MINOR.PATCH(-prerelease)?", m.Plugin.Version)
	}
	if _, err := ExportManifestJSON(m, doc); err != nil {
		return nil, fmt.Errorf("contract derivation failed: %w", err)
	}
	caps := make([]string, 0, len(m.Implements))
	for _, c := range m.Implements {
		caps = append(caps, c.Capability)
	}
	langs := make([]string, 0, len(m.Locales))
	for l := range m.Locales {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	var deploy []string
	if m.Deployment != nil {
		for _, t := range m.Deployment.Targets {
			deploy = append(deploy, t.Kind)
		}
	}
	return &IndexEntry{
		Ref: org + "/" + name, Org: org, Name: name,
		Label: m.Plugin.Label, Desc: m.Plugin.Desc, Version: m.Plugin.Version,
		Capabilities: caps,
		Operations:   len(m.AllOperations()), Events: len(m.Events),
		Langs: langs, Deploy: deploy,
	}, nil
}

// ValidVersionExpr reports whether one advisories.json versions[] item is well-formed: an exact
// version, or a range with a <, <=, > or >= prefix. A malformed expression must be rejected at the
// gate — the platform's matcher treats what it cannot parse conservatively, which silently changes
// who the advisory reaches.
func ValidVersionExpr(s string) bool {
	s = strings.TrimSpace(s)
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if strings.HasPrefix(s, op) {
			return distVersionRe.MatchString(strings.TrimSpace(strings.TrimPrefix(s, op)))
		}
	}
	return distVersionRe.MatchString(s)
}
