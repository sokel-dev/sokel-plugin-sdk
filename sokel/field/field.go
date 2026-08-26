// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package field forwards to plugin-core/contract/field.
//
// The builders moved down into plugin-core along with the contract, since they use only contract types
// and involve no transport. Keeping the sokel/field import path here means existing plugins need no
// change.
package field

import "github.com/sokel-dev/sokel-plugin-sdk/contract/field"

// B is the field builder.
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
	Secret  = field.Secret // credentials only: a masked field
	Select  = field.Select // credentials only: a dropdown
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
