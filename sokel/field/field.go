// Package field 是 plugin-core/contract/field 的转发。
//
// builder 已随契约下沉到 plugin-core（它只用契约类型，不含传输），
// 这里保留 sokel/field 这个导入路径，让既有插件一行不用改。
package field

import "github.com/sokel-dev/sokel-plugin-sdk/contract/field"

// B：字段 builder。
type B = field.B

var (
	String  = field.String
	Text    = field.Text
	Number  = field.Number
	Int     = field.Int
	Bool    = field.Bool
	File    = field.File
	Files   = field.Files
	Enum    = field.Enum
	Secret  = field.Secret // 凭证专用：密文字段
	Select  = field.Select // 凭证专用：下拉选择
	Opt     = field.Opt
	Json    = field.Json
	Array   = field.Array
	ArrayOf = field.ArrayOf
	Strings = field.Strings
	Numbers = field.Numbers
	Ints    = field.Ints
	Bools   = field.Bools
	OneOf   = field.OneOf
	Any     = field.Any
	Object  = field.Object
)
