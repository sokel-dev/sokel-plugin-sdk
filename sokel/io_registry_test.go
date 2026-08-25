package sokel

import "testing"

type ioTestIn struct {
	X string `sokel:"x"`
}
type ioTestOut struct {
	Y string `sokel:"y"`
}
type ioOtherIn struct{}

// 生成的 zz_sokel.go 通过 RegisterIO 把契约注册进表，Register 据此查表而不再反射。
// 类型参数不是装饰：它把「生成物」与「struct」在**编译期**绑在一起——
// struct 改了名不重新生成，代码根本编不过；改了字段但没重新生成，则由类型校验兜住。
func TestRegisterIOLookup(t *testing.T) {
	resetIORegistry()
	io := IO{Inputs: []Field{{Name: "x", Type: TString}}, Outputs: []Field{{Name: "y", Type: TString}}}
	RegisterIO[ioTestIn, ioTestOut]("op_a", io)

	got, ok := lookupIO[ioTestIn, ioTestOut]("op_a")
	if !ok {
		t.Fatal("注册过的操作应查得到")
	}
	if len(got.Inputs) != 1 || got.Inputs[0].Name != "x" {
		t.Errorf("查到的契约不对: %+v", got)
	}

	// 没注册过 → 查不到（调用方据此回退或提示跑 go generate）
	if _, ok := lookupIO[ioTestIn, ioTestOut]("op_missing"); ok {
		t.Error("未注册的操作不该查到")
	}

	// 类型对不上 → 视为查不到。这正是「生成物过期」的信号：
	// 作者把入参 struct 换成了别的类型，但 zz_sokel.go 还是老的。
	if _, ok := lookupIO[ioOtherIn, ioTestOut]("op_a"); ok {
		t.Error("类型不匹配时不该返回过期契约")
	}
}
