// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import "reflect"

// IO is one operation's input/output contract. The generated zz_sokel.go registers it in an init
// function and Register looks it up here, in place of runtime reflection.
type IO struct {
	Inputs  []Field
	Outputs []Field
}

type ioEntry struct {
	io  IO
	in  reflect.Type
	out reflect.Type
}

var ioRegistry = map[string]ioEntry{}

// RegisterIO is called by generated code to record an operation's contract.
//
// The type parameters are not decoration: they tie the generated file to the struct **at compile
// time**. Rename the struct without regenerating and zz_sokel.go stops compiling; swap in a different
// type and lookupIO's type check treats it as absent, so the caller asks for a regeneration.
// Generated code drifting silently from the source is the classic codegen trap.
func RegisterIO[In any, Out any](opID string, io IO) {
	ioRegistry[opID] = ioEntry{
		io:  io,
		in:  reflect.TypeOf((*In)(nil)).Elem(),
		out: reflect.TypeOf((*Out)(nil)).Elem(),
	}
}

// lookupIO fetches an operation's contract. A type mismatch counts as absent: the generated file is
// out of date.
func lookupIO[In any, Out any](opID string) (IO, bool) {
	e, ok := ioRegistry[opID]
	if !ok {
		return IO{}, false
	}
	if e.in != reflect.TypeOf((*In)(nil)).Elem() || e.out != reflect.TypeOf((*Out)(nil)).Elem() {
		return IO{}, false
	}
	return e.io, true
}

// resetIORegistry is for tests only: the registry is package-level state and cases must not leak
// into each other.
func resetIORegistry() { ioRegistry = map[string]ioEntry{} }
