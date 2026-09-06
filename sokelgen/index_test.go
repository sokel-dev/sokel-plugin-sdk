package sokelgen

import (
	"strings"
	"testing"
)

func TestBuildIndexEntryDerivesStats(t *testing.T) {
	m, err := ParseManifest([]byte(`{
		"plugin":{"name":"demo","org":"acme","label":"Demo","desc":"d","version":"1.2.0"},
		"operations":[{"id":"a","label":"A","inputs":[],"outputs":[]},{"id":"b","label":"B","inputs":[],"outputs":[]}],
		"events":[{"id":"e1","label":"E1","fields":[]}],
		"deployment":{"targets":[{"kind":"container","ref":"acme/demo:1.2.0"}]}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	m.Locales = map[string]map[string]string{"en": {"Demo": "Demo (en)", "d": "desc (en)"}}
	e, err := BuildIndexEntry(m, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Ref != "acme/demo" || e.Operations != 2 || e.Events != 1 ||
		len(e.Langs) != 1 || e.Langs[0] != "en" || len(e.Deploy) != 1 || e.Deploy[0] != "container" {
		t.Fatalf("推导不对: %+v", e)
	}
	if e.Manifest != "" || e.Files != nil {
		t.Error("路径字段归调用方，这里必须留空")
	}
	if e.I18n["en"].Label != "Demo (en)" || e.I18n["en"].Desc != "desc (en)" {
		t.Errorf("卡片级 i18n 该从 locale 表按原文串查出: %+v", e.I18n)
	}
}

func TestBuildIndexEntryVersionRequired(t *testing.T) {
	m, err := ParseManifest([]byte(`{"plugin":{"name":"demo","org":"acme","label":"Demo"},
		"operations":[{"id":"a","label":"A","inputs":[],"outputs":[]}]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildIndexEntry(m, ""); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("分发门必须要求版本（manifest 里可缺，进目录不行）: %v", err)
	}
}

func TestValidVersionExpr(t *testing.T) {
	for _, ok := range []string{"1.2.3", "v1.2.3", "<2.0.0", ">=v1.0.0", "1.0.0-rc.1"} {
		if !ValidVersionExpr(ok) {
			t.Errorf("%q 该合法", ok)
		}
	}
	for _, bad := range []string{"", "abc", "^1.2.3", "< abc", "1.2", "=1.2.3"} {
		if ValidVersionExpr(bad) {
			t.Errorf("%q 该拒收", bad)
		}
	}
}
