// 由 sokel-gen 从旧的 struct+tag 契约反向生成——**这是迁移起点，不是终点**。
// 过一遍再提交：
//   1. 无结构的 json/array 现在标成了 Opaque("待补理由")，逐个判断是补结构还是写清理由
//   2. 下面引用的类型需要从 main 包挪到本包（schema 只声明，不该反向依赖实现）
//      待挪动：Memory
//   3. Label/Desc 若原本塞在 desc 里做「值=含义」对照（如发音人列表），改用 field.Opt 的显示名

package schema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel/field"
)

// 内存统计（嵌套 json 输出，子字段按 sokel 名展开）。
type Memory struct {
	AllocBytes     uint64 `sokel:"alloc_bytes" label:"已分配"`
	SysBytes       uint64 `sokel:"sys_bytes" label:"系统占用"`
	HeapAllocBytes uint64 `sokel:"heap_alloc_bytes" label:"堆已分配"`
}

// FileDigest （迁移自旧契约）
type FileDigest struct{}

func (FileDigest) Meta() sokel.Meta {
	return sokel.Meta{ID: "file_digest"}
}

func (FileDigest) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.File("file").Label("文件").Desc("任意文件，计算其 md5 与大小"),
	}
}

func (FileDigest) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.String("filename").Label("文件名"),
		field.String("md5").Label("MD5"),
		field.Int("size").Label("字节数"),
	}
}

// SystemInfo （迁移自旧契约）
type SystemInfo struct{}

func (SystemInfo) Meta() sokel.Meta {
	return sokel.Meta{ID: "system_info"}
}

func (SystemInfo) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.Bool("include_memory").Label("包含内存统计").Desc("关闭则输出不含 memory").Default(true),
		field.String("note").Label("备注（回显）").Desc("原样回显到 echo —— 验证入参→出参逐字段流转").Optional(),
	}
}

func (SystemInfo) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.Bool("ok").Label("成功"),
		field.String("hostname").Label("主机名"),
		field.String("os").Label("操作系统"),
		field.String("arch").Label("架构"),
		field.Int("num_cpu").Label("CPU 核数"),
		field.String("go_version").Label("Go 版本"),
		field.Int("pid").Label("进程号"),
		field.Int("goroutines").Label("协程数"),
		field.Int("uptime_seconds").Label("运行时长(秒)"),
		field.String("started_at").Label("启动时间"),
		field.String("now").Label("当前时间"),
		field.String("echo").Label("回显备注"),
		field.Json("memory", Memory{}).Label("内存统计").Optional(),
	}
}
