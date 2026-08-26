// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"reflect"
	"strconv"
	"strings"
)

// reflectStructTag reuses the standard library's tag parsing, so the `a:"x" b:"y"` rules need not be
// written out a second time.
func reflectStructTag(tag string) reflect.StructTag { return reflect.StructTag(tag) }

// toSnake follows the same rule as the SDK: with no sokel tag, use the field name lowered with
// underscores.
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

// coerceDefault converts a default tag's string into a real value according to the contract type,
// with the same semantics as the SDK.
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
