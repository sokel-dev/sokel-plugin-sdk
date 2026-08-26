// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"reflect"
	"strconv"
	"strings"
)

// reflectStructTag：复用标准库的 tag 解析（`a:"x" b:"y"` 的规则不必自己写一遍）。
func reflectStructTag(tag string) reflect.StructTag { return reflect.StructTag(tag) }

// toSnake 与 sokel 侧同规则：无 sokel tag 时用字段名的下划线小写形式。
func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// coerceDefault 把 default tag 的字符串按契约类型转成真值（与 sokel 侧同语义）。
func coerceDefault(v, typ string) any {
	switch typ {
	case "number":
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true
		case "false", "0":
			return false
		}
	}
	return v
}
