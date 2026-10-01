package sokel

import (
	"encoding/json"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// A capability-slot operation (what `implements:` in a manifest produces, e.g. rowstore.query) has a
// platform-derived wire id that contains a dot. Applying the business-id rule to it made the official
// kitchen-sink example panic at startup. Plain business operations still may not contain a dot.
func TestRegisterOpAllowsCapabilitySlotIDs(t *testing.T) {
	noop := func(Ctx, json.RawMessage, plugin.Sink) error { return nil }

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("capability slot rowstore.query must not panic: %v", r)
			}
		}()
		RegisterOp(&Plugin{}, contract.Operation{ID: "rowstore.query", Capability: "rowstore.query"}, noop)
	}()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("a dotted id without a capability must still panic")
			}
		}()
		RegisterOp(&Plugin{}, contract.Operation{ID: "rowstore.query"}, noop)
	}()
}
