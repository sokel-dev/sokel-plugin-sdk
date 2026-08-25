package auth

import (
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// 步骤由形态决定，不该让调用方再抄一遍——抄错的那份没人会发现：
// 多写一步 = 承诺一份永远不会被调用的实现；少写一步 = 面板卡在缺的那一步。
func TestStepsFollowFromKind(t *testing.T) {
	cases := []struct {
		name  string
		meta  contract.AuthMeta
		kind  contract.AuthKind
		steps []contract.AuthStep
	}{
		{"扫码", QR(), contract.AuthQR,
			[]contract.AuthStep{contract.StepStart, contract.StepPoll}},
		// 回填比扫码多一步 submit——那一步正是这种形态的全部意义
		{"回填", Input(), contract.AuthInput,
			[]contract.AuthStep{contract.StepStart, contract.StepPoll, contract.StepSubmit}},
		// OAuth 一步都没有：client_secret 在平台手里，start/poll 全程平台代答
		{"OAuth", OAuth("google", "s1", "s2"), contract.AuthOAuth, nil},
	}
	for _, c := range cases {
		if c.meta.Kind != c.kind {
			t.Errorf("%s: kind = %q, want %q", c.name, c.meta.Kind, c.kind)
		}
		if len(c.meta.Steps) != len(c.steps) {
			t.Fatalf("%s: steps = %v, want %v", c.name, c.meta.Steps, c.steps)
		}
		for i, s := range c.steps {
			if c.meta.Steps[i] != s {
				t.Errorf("%s: steps[%d] = %q, want %q", c.name, i, c.meta.Steps[i], s)
			}
		}
	}
}

// 作用域作必填参数：OAuth 的最小权限就体现在这儿，留空等于要一个必然被拒的同意页。
func TestOAuthCarriesProviderAndScopes(t *testing.T) {
	m := OAuth("google", "https://www.googleapis.com/auth/gmail.readonly")
	if m.Provider != "google" || len(m.Scopes) != 1 {
		t.Fatalf("provider/scopes 没带上: %+v", m)
	}
}
