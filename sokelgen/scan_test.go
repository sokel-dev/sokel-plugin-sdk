package sokelgen

import "testing"

// 扫描 sokel.Register 调用点，取出「操作 id ↔ 入/出参类型」。
// 真实插件里类型参数是**推断**的（sokel.Register(p, op, handler)，不写 [In, Out]），
// 所以得从 handler 签名反推：func(sokel.Ctx, In, *sokel.Emitter[Out]) error。
// 两种 handler 形态都要认——内联闭包（sysinfo）与具名函数（report-pipeline）。
const scanSrc = `package main

import "github.com/sokel-dev/sokel-plugin-sdk/sokel"

type SysInfoIn struct{}
type SysInfoOut struct{}
type PreIn struct{}
type PreOut struct{}

func preprocess(_ sokel.Ctx, in PreIn, out *sokel.Emitter[PreOut]) error { return nil }

func main() {
	p := sokel.New(sokel.Config{})
	// 内联闭包
	sokel.Register(p, sokel.Operation{ID: "system_info", Label: "系统信息"},
		func(ctx sokel.Ctx, in SysInfoIn, out *sokel.Emitter[SysInfoOut]) error { return nil })
	// 具名函数
	sokel.Register(p, sokel.Operation{ID: "preprocess", Label: "预处理"}, preprocess)
	_ = p
}
`

func TestScanRegisterCalls(t *testing.T) {
	ops, err := ScanOps(scanSrc)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("应扫到 2 个操作: %+v", ops)
	}
	byID := map[string]OpIO{}
	for _, o := range ops {
		byID[o.OpID] = o
	}
	if got := byID["system_info"]; got.InType != "SysInfoIn" || got.OutType != "SysInfoOut" {
		t.Errorf("内联闭包的类型没反推对: %+v", got)
	}
	if got := byID["preprocess"]; got.InType != "PreIn" || got.OutType != "PreOut" {
		t.Errorf("具名 handler 的类型没反推对: %+v", got)
	}
}

// 操作 id 不是字面量（拿变量拼的）→ 生成期报错。
// 静默跳过的后果是那个操作的契约永远不出现，插件启动后才发现「这个操作不见了」。
func TestScanRegisterNonLiteralID(t *testing.T) {
	src := `package main

import "github.com/sokel-dev/sokel-plugin-sdk/sokel"

type In struct{}
type Out struct{}

var opID = "dynamic"

func main() {
	p := sokel.New(sokel.Config{})
	sokel.Register(p, sokel.Operation{ID: opID}, func(ctx sokel.Ctx, in In, out *sokel.Emitter[Out]) error { return nil })
	_ = p
}
`
	if _, err := ScanOps(src); err == nil {
		t.Fatal("非字面量 id 应报错，不能静默跳过")
	}
}
