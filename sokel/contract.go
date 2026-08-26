// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"reflect"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// The contract types are defined in the contract package, shared by the platform and the SDK rather
// than written twice. What follows is alias forwarding, so plugin authors keep writing sokel.Field
// and sokel.DeriveFields unchanged.
//
// Why it moved: the SDK's Field used to be the complete one while the platform kept a cut-down
// version with only name/type/fields/valueType for normalisation and pre-run validation. Anything
// the SDK declared that the cut-down copy lacked — unions, enums, required, oneOf, multiple — was
// invisible to the platform.

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

// Lowercase names kept for existing call sites in this package (deriveFields / parseSokelTag /
// applyDefaultTag). Contract derivation moved out; these forward so a dozen call sites need not change.
func deriveFields(t reflect.Type) []Field { return contract.DeriveFields(t) }

func parseSokelTag(sf reflect.StructField) (string, bool) { return contract.ParseTag(sf) }

func applyDefaultTag(v reflect.Value, sf reflect.StructField) { contract.ApplyDefaultTag(v, sf) }

// DeriveFields derives contract fields from an input/output struct by reflection.
func DeriveFields(t reflect.Type) []Field { return contract.DeriveFields(t) }

// BuildFields expands declarative FieldSpecs into contract fields.
func BuildFields(specs []FieldSpec) []Field { return contract.BuildFields(specs) }

// OperationOf produces an operation contract from a Schema declaration.
func OperationOf(s Schema) Operation { return contract.OperationOf(s) }

// BindInput binds the platform's input JSON into an input struct **recursively**, by sokel tag.
//
// Do not substitute json.Unmarshal: it only knows json tags and Go field names, so a snake_case
// contract field inside a nested struct binds to nothing at all (doc_id never reaches DocID).
func BindInput(input json.RawMessage, dst any) error { return contract.BindInput(input, dst) }

// StructToVars expands an output struct into {contract name: value}, recursively, by sokel tag.
func StructToVars(o any) map[string]any { return contract.StructToVars(o) }

func bindInput(input json.RawMessage, dst any) error { return contract.BindInput(input, dst) }
func structToVars(o any) map[string]any              { return contract.StructToVars(o) }
