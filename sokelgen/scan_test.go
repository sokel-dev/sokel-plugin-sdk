// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import "testing"

// Scanning sokel.Register call sites for the mapping from operation id to input/output types. In real
// plugins the type arguments are **inferred** — sokel.Register(p, op, handler) with no [In, Out] — so
// they have to be read back out of the handler's signature: func(sokel.Ctx, In, *sokel.Emitter[Out])
// error. Both handler forms must be recognised: an inline closure and a named function.
const scanSrc = `package main

import "github.com/sokel-dev/sokel-plugin-sdk/sokel"

type SysInfoIn struct{}
type SysInfoOut struct{}
type PreIn struct{}
type PreOut struct{}

func preprocess(_ sokel.Ctx, in PreIn, out *sokel.Emitter[PreOut]) error { return nil }

func main() {
	p := sokel.New(sokel.Config{})
	// an inline closure
	sokel.Register(p, sokel.Operation{ID: "system_info", Label: "System info"},
		func(ctx sokel.Ctx, in SysInfoIn, out *sokel.Emitter[SysInfoOut]) error { return nil })
	// a named function
	sokel.Register(p, sokel.Operation{ID: "preprocess", Label: "Preprocess"}, preprocess)
	_ = p
}
`

func TestScanRegisterCalls(t *testing.T) {
	ops, err := ScanOps(scanSrc)
	if err != nil {
		t.Fatalf("the scan failed: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("2 operations should have been found: %+v", ops)
	}
	byID := map[string]OpIO{}
	for _, o := range ops {
		byID[o.OpID] = o
	}
	if got := byID["system_info"]; got.InType != "SysInfoIn" || got.OutType != "SysInfoOut" {
		t.Errorf("the inline closure's types were inferred wrongly: %+v", got)
	}
	if got := byID["preprocess"]; got.InType != "PreIn" || got.OutType != "PreOut" {
		t.Errorf("the named handler's types were inferred wrongly: %+v", got)
	}
}

// An operation id that is not a literal, built from variables, fails at generation time. Skipping it
// silently would mean the contract for that operation never appears, and the author would notice it
// missing only after starting the plugin.
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
		t.Fatal("a non-literal id should fail rather than be skipped silently")
	}
}
