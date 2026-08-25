package pluginenv

import "testing"

// 前缀收在一处：插件不该各写各的 "SOKEL_TOKEN" 字面量。
func TestGetReadsPrefixedName(t *testing.T) {
	t.Setenv("SOKEL_TOKEN", " v ")
	if got := Get("TOKEN"); got != "v" {
		t.Errorf("应读 SOKEL_TOKEN 并去空白，实得 %q", got)
	}
}

// 只认 SOKEL_ 前缀：别的前缀一律读不到，免得兼容层变成没人摘的历史包袱。
func TestGetHasNoLegacyFallback(t *testing.T) {
	t.Setenv("PLUGIN_TOKEN", "legacy")
	t.Setenv("OTHER_TOKEN", "older")
	if got := Get("TOKEN"); got != "" {
		t.Errorf("不该再认老前缀，实得 %q", got)
	}
}
