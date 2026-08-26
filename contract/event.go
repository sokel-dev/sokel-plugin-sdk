// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import "fmt"

// The declarative form of an event contract.
//
// Events used to be declarable only imperatively (DeclareEvent[T] plus reflection over the payload),
// which meant three event-source plugins **could not** move to the declarative style — not that
// nobody had got round to it. With this, operations and events are declared the same way: methods on
// a type, read by the generator.

// Event is one event and the contract of its payload, reported to the platform at registration.
type Event struct {
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	Desc   string  `json:"desc,omitempty"`
	Fields []Field `json:"fields"`
}

// EventMeta is an event's identity, the counterpart of Meta for an operation.
type EventMeta struct {
	ID    string
	Label string
	Desc  string
}

// EventSchema is one event's declaration, mirroring Schema for operations: a misspelled method name
// fails to compile instead of surfacing at generation time.
type EventSchema interface {
	EventMeta() EventMeta
	Fields() []FieldSpec
}

// EventOf produces an event contract from a declaration.
func EventOf(e EventSchema) Event {
	m := e.EventMeta()
	fields := BuildFields(e.Fields())
	if fields == nil {
		fields = []Field{} // an empty array rather than null, so nothing downstream guards against null
	}
	return Event{ID: m.ID, Label: m.Label, Desc: m.Desc, Fields: fields}
}

// CommonFieldsSchema optionally declares the fields every event shares.
//
// On trigger the platform flattens them from the payload to the top level of the input, so every
// event branch references one variable. They must be **declared explicitly** rather than inferred as
// the intersection: with inference, adding an event that happens to omit a field silently shrinks the
// set and breaks existing workflows — and nobody would think to look here.
type CommonFieldsSchema interface {
	CommonFields() []string
}

// Reserved keys the platform uses at the top level of a trigger input; a common field may not
// collide with them.
var eventReserved = map[string]bool{
	"_event": true, "event": true, "input": true, "credential_id": true,
}

// ValidateCommonFields checks that each common field exists in **every** event with the same type.
//
// It fails fast rather than silently taking the intersection, for the reason above.
func ValidateCommonFields(events []Event, names []string) ([]Field, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("common fields are declared but there are no events")
	}
	ids := map[string]bool{}
	for _, e := range events {
		ids[e.ID] = true
	}
	out := make([]Field, 0, len(names))
	for _, name := range names {
		if eventReserved[name] {
			return nil, fmt.Errorf("common field %q collides with a platform-reserved key", name)
		}
		if ids[name] {
			return nil, fmt.Errorf("common field %q collides with an event id", name)
		}
		var ref *Field
		for _, e := range events {
			f := findField(e.Fields, name)
			if f == nil {
				return nil, fmt.Errorf("common field %q is not declared in event %q", name, e.ID)
			}
			if ref == nil {
				ref = f
				continue
			}
			if f.Type != ref.Type {
				return nil, fmt.Errorf("common field %q has type %s in event %q, which differs from %s elsewhere",
					name, f.Type, e.ID, ref.Type)
			}
		}
		out = append(out, *ref)
	}
	return out, nil
}

func findField(fs []Field, name string) *Field {
	for i := range fs {
		if fs[i].Name == name {
			return &fs[i]
		}
	}
	return nil
}
