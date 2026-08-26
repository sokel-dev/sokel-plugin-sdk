// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import "testing"

type ioTestIn struct {
	X string `sokel:"x"`
}
type ioTestOut struct {
	Y string `sokel:"y"`
}
type ioOtherIn struct{}

// The generated zz_sokel.go registers contracts into a table via RegisterIO, and Register looks them up
// there instead of reflecting.
// The type parameters are not decoration: they bind the generated code to the struct **at compile
// time** — rename the struct without regenerating and the code does not build at all, and change its
// fields without regenerating and the type check catches it.
func TestRegisterIOLookup(t *testing.T) {
	resetIORegistry()
	io := IO{Inputs: []Field{{Name: "x", Type: TString}}, Outputs: []Field{{Name: "y", Type: TString}}}
	RegisterIO[ioTestIn, ioTestOut]("op_a", io)

	got, ok := lookupIO[ioTestIn, ioTestOut]("op_a")
	if !ok {
		t.Fatal("a registered operation should be found")
	}
	if len(got.Inputs) != 1 || got.Inputs[0].Name != "x" {
		t.Errorf("the contract found is wrong: %+v", got)
	}

	// Unregistered means not found, on which the caller falls back or suggests running go generate
	if _, ok := lookupIO[ioTestIn, ioTestOut]("op_missing"); ok {
		t.Error("an unregistered operation should not be found")
	}

	// A type mismatch counts as not found, which is precisely the signal that the generated code is stale:
	// the author swapped the input struct for another type while zz_sokel.go stayed as it was.
	if _, ok := lookupIO[ioOtherIn, ioTestOut]("op_a"); ok {
		t.Error("a stale contract must not be returned on a type mismatch")
	}
}
