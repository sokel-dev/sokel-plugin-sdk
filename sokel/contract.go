package sokel

import (
	"encoding/json"
	"reflect"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// 契约的定义在 plugin-core/contract —— 平台与 SDK 共用同一份，不再各写一份。
// 这里只是别名转发：插件作者继续写 sokel.Field / sokel.DeriveFields，一行不用改。
//
// 为什么下沉：此前 SDK 的 Field 是全量的，而平台侧另有一个只含
// name/type/fields/valueType 的简版，用它做类型归一与运行前校验。
// 于是 SDK 声明了而那份没有的东西（联合类型、枚举、必填、oneOf、multiple）平台看不见。

type (
	ParamType    = contract.ParamType
	Field        = contract.Field
	Option       = contract.Option
	OneOfVariant = contract.OneOfVariant
	FieldSpec    = contract.FieldSpec
	Meta         = contract.Meta
	Schema       = contract.Schema
	Operation    = contract.Operation
)

const (
	TString ParamType = contract.TString
	TNumber ParamType = contract.TNumber
	TBool   ParamType = contract.TBool
	TJSON   ParamType = contract.TJSON
	TArray  ParamType = contract.TArray
	TFile   ParamType = contract.TFile
	TEnum   ParamType = contract.TEnum
)

// 包内旧调用点沿用的小写名（deriveFields / parseSokelTag / applyDefaultTag）：
// 契约推导已下沉，这里转发，免得把 sokel 里十几处调用全改一遍。
func deriveFields(t reflect.Type) []Field { return contract.DeriveFields(t) }

func parseSokelTag(sf reflect.StructField) (string, bool) { return contract.ParseTag(sf) }

func applyDefaultTag(v reflect.Value, sf reflect.StructField) { contract.ApplyDefaultTag(v, sf) }

// DeriveFields 从入/出参 struct 反射推导契约字段。
func DeriveFields(t reflect.Type) []Field { return contract.DeriveFields(t) }

// BuildFields 把声明式 FieldSpec 展开成契约字段。
func BuildFields(specs []FieldSpec) []Field { return contract.BuildFields(specs) }

// OperationOf 由 Schema 声明产出操作契约。
func OperationOf(s Schema) Operation { return contract.OperationOf(s) }

// BindInput 把平台传来的 input JSON 按 sokel tag **递归**绑进入参 struct。
// 不要用 json.Unmarshal 代替：那只认 json tag / Go 字段名，
// 嵌套结构里 snake_case 的契约字段会静默绑空（doc_id 落不进 DocID）。
func BindInput(input json.RawMessage, dst any) error { return contract.BindInput(input, dst) }

// StructToVars 把出参 struct 按 sokel tag 递归展成 {契约名: 值}。
func StructToVars(o any) map[string]any { return contract.StructToVars(o) }

func bindInput(input json.RawMessage, dst any) error { return contract.BindInput(input, dst) }
func structToVars(o any) map[string]any              { return contract.StructToVars(o) }
