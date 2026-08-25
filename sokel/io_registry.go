package sokel

import "reflect"

// IO：一个操作的入/出参契约。由 sokel-gen 生成的 zz_sokel.go 在 init 里注册进来，
// Register 据此查表——取代运行时反射（docs/plugin-sdk-multilang.md §1）。
type IO struct {
	Inputs  []Field
	Outputs []Field
}

type ioEntry struct {
	io  IO
	in  reflect.Type
	out reflect.Type
}

var ioRegistry = map[string]ioEntry{}

// RegisterIO 由生成物调用，登记某操作的契约。
//
// 类型参数不是装饰：它把生成物与 struct 在**编译期**绑住。struct 改了名而没重新生成，
// zz_sokel.go 直接编译不过；换成了别的类型，则由 lookupIO 的类型校验兜住（视为查不到，
// 调用方会提示重新生成）——生成物与源码悄悄不同步是 codegen 最典型的坑。
func RegisterIO[In any, Out any](opID string, io IO) {
	ioRegistry[opID] = ioEntry{
		io:  io,
		in:  reflect.TypeOf((*In)(nil)).Elem(),
		out: reflect.TypeOf((*Out)(nil)).Elem(),
	}
}

// lookupIO 取某操作的契约；类型对不上一律当作查不到（生成物过期）。
func lookupIO[In any, Out any](opID string) (IO, bool) {
	e, ok := ioRegistry[opID]
	if !ok {
		return IO{}, false
	}
	if e.in != reflect.TypeOf((*In)(nil)).Elem() || e.out != reflect.TypeOf((*Out)(nil)).Elem() {
		return IO{}, false
	}
	return e.io, true
}

// resetIORegistry 仅供测试：注册表是包级状态，用例之间必须隔离。
func resetIORegistry() { ioRegistry = map[string]ioEntry{} }
