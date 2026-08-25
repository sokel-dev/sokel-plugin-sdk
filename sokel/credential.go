package sokel

import "reflect"

// CredentialAs 把本次调用平台下发的凭证 map 按 sokel 标签绑定到类型化结构体 T，返回填好的值。
//
// 取代 ctx.Credential()["key"] 的裸 map 访问：结构体即契约、编译期字段名安全、单一事实源，
// 与操作入参 In/Out 的声明方式一致（同一套 sokel 标签 + 反射）。
//
//	type Cred struct {
//	    BaseURL   string `sokel:"base_url"   label:"服务基地址"`
//	    XSuperUID string `sokel:"x_super_uid" label:"x-super-uid 头" default:"7"`
//	}
//	cred := sokel.CredentialAs[Cred](ctx)   // cred.BaseURL / cred.XSuperUID
//
// 规则：字段名取 `sokel:"name"`（缺省用字段名的下划线小写）；仅绑定 string 字段（凭证值均为字符串）；
// 平台未下发该键、或值为空时，回退到 `default:"..."` 标签（无则留零值）。
func CredentialAs[T any](ctx Ctx) T {
	var out T
	bindCredential(ctx.Credential(), &out)
	return out
}

// SourceCredentialAs：事件源侧的类型化读取（与操作侧 CredentialAs 同义）。
//
// 事件源拿的是 SourceCtx（不是 Ctx），早先只能 ctx.Credential()["裸键"]——
// 而事件源恰恰是最常读写凭证的地方（游标、会话），裸键拼错就是静默绑空。
func SourceCredentialAs[T any](ctx SourceCtx) T {
	var out T
	bindCredential(ctx.Credential(), &out)
	return out
}

// BindCredential 与 CredentialAs 同义的指针版：绑定到调用方已有的结构体（便于就地复用）。
// BindCredential 绑定到调用方已有的结构体（便于就地复用）。
func BindCredential(ctx Ctx, dst any) { bindCredential(ctx.Credential(), dst) }

// bindCredential 反射 dst 结构体，按 sokel 标签名从凭证 map 取值填入 string 字段；
// 缺省/空值走 `default:"..."` 标签（复用 applyDefaultTag，与入参绑定一致）。
func bindCredential(cred map[string]string, dst any) {
	v := reflect.ValueOf(dst).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, _ := parseAcnTag(sf)
		if name == "-" {
			continue
		}
		fv := v.Field(i)
		if val, ok := cred[name]; ok && val != "" && fv.Kind() == reflect.String && fv.CanSet() {
			fv.SetString(val)
			continue
		}
		applyDefaultTag(fv, sf) // 缺省/空 → default 标签（无则零值）
	}
}

// SetCredentialContract 实现 plugin.CredentialHost：直接接住一份**已声明好**的凭证契约。
//
// 给 sokel-gen 生成的 RegisterCredential 用——schema 声明能表达 enum 候选值、默认值这些
// struct tag 写不出来的东西，所以走这条路时不再回头做反射。
func (p *Plugin) SetCredentialContract(fields []Field) { p.credFields = fields }

// SetDoc 实现 plugin.DocHost：接住使用说明（markdown / 外链，给一个即可）。
//
// 建议把说明写成真的 .md 文件用 //go:embed 嵌进来——反引号字符串里全是代码块与反引号，
// 改一句话得先想怎么闭合。内核插件（plugin-core/searchcore）就是这么做的，可作模板。
func (p *Plugin) SetDoc(markdown, url string) { p.doc, p.docURL = markdown, url }

// OAuthSpec 声明「本插件的凭证经某个 OAuth 提供方获取」。
type OAuthSpec struct {
	Provider string   `json:"provider"` // 目前支持 "google"
	Scopes   []string `json:"scopes"`   // 要申请的作用域（**由插件声明**，平台不写死）
}
