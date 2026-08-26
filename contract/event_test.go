// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"strings"
	"testing"
)

type fileCreated struct{}

func (fileCreated) EventMeta() EventMeta { return EventMeta{ID: "file_created", Label: "New file"} }
func (fileCreated) Fields() []FieldSpec {
	return []FieldSpec{specOf(Field{Name: "path", Type: TString, Required: true}), specOf(Field{Name: "size", Type: TNumber})}
}

type fileDeleted struct{}

func (fileDeleted) EventMeta() EventMeta { return EventMeta{ID: "file_deleted"} }
func (fileDeleted) Fields() []FieldSpec {
	return []FieldSpec{specOf(Field{Name: "path", Type: TString, Required: true})}
}

type fieldSpecFn func() Field

func (f fieldSpecFn) Field() Field { return f() }

func specOf(f Field) FieldSpec { return fieldSpecFn(func() Field { return f }) }

func TestEventOf(t *testing.T) {
	e := EventOf(fileCreated{})
	if e.ID != "file_created" || e.Label != "New file" || len(e.Fields) != 2 {
		t.Fatalf("event contract: %+v", e)
	}
	// No fields means an empty array rather than null, so downstream need not guard against null
	type empty struct{ EventSchema }
	if got := EventOf(noFields{}); got.Fields == nil {
		t.Errorf("no fields should be an empty array: %+v", got)
	}
	_ = empty{}
}

type noFields struct{}

func (noFields) EventMeta() EventMeta { return EventMeta{ID: "x"} }
func (noFields) Fields() []FieldSpec  { return nil }

// Common fields have to exist in **every** event with the same type. They are not intersected — with an
// intersection, adding an event that omits a field would silently shrink the set and break existing
// workflows.
func TestValidateCommonFields(t *testing.T) {
	events := []Event{EventOf(fileCreated{}), EventOf(fileDeleted{})}

	got, err := ValidateCommonFields(events, []string{"path"})
	if err != nil || len(got) != 1 || got[0].Name != "path" {
		t.Fatalf("path exists in both events and should pass: %+v %v", got, err)
	}

	// size exists only in file_created, so it fails and names the event that lacks it
	_, err = ValidateCommonFields(events, []string{"size"})
	if err == nil || !strings.Contains(err.Error(), "file_deleted") {
		t.Errorf("the error should name the event missing the field: %v", err)
	}
	// Colliding with a platform-reserved key
	if _, err := ValidateCommonFields(events, []string{"event"}); err == nil ||
		!strings.Contains(err.Error(), "reserved") {
		t.Errorf("a reserved key should be rejected: %v", err)
	}
	// Colliding with an event id
	if _, err := ValidateCommonFields(events, []string{"file_created"}); err == nil ||
		!strings.Contains(err.Error(), "event id") {
		t.Errorf("a collision with an event id should be rejected: %v", err)
	}
	// mismatched types
	mixed := []Event{
		{ID: "a", Fields: []Field{{Name: "k", Type: TString}}},
		{ID: "b", Fields: []Field{{Name: "k", Type: TNumber}}},
	}
	if _, err := ValidateCommonFields(mixed, []string{"k"}); err == nil ||
		!strings.Contains(err.Error(), "differs from") {
		t.Errorf("mismatched types should be rejected: %v", err)
	}
}
