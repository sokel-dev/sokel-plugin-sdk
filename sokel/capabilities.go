// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// Capability self-report: the plugin tells the platform which **optional** capabilities of a shared
// contract it does not actually support.
//
// Why this exists (it surfaced while writing a second storage backend): one contract can have several
// implementations, and the difference between them is often not "does this operation exist" but
// "there is an optional input of this operation I cannot honour". Recency weighting in the storage
// contract was exactly that — one engine has a built-in for it, the other needed a hand-written decay
// expression that the first version skipped. With nowhere in the contract to say so, the plugin could
// only **silently ignore** the input: the platform could neither degrade nor warn, so a user turned
// recency on, the UI looked fine, and nothing in the results reflected it.
//
// Division of labour with operations: whether an operation exists is in operations; **how far it
// goes** is here.

// Optional capabilities of a knowledge-base storage engine. Constants rather than bare strings: one
// wrong letter means "declared but ineffective", which is this mechanism's classic silent failure.
// If the compiler can catch it, do not leave it to runtime.
const (
	// CapRecency: keyword_query supports recency weighting (the closer to the pivot, the higher).
	CapRecency = "recency"
	// CapTimeRange: retrieval supports time_range filtering.
	CapTimeRange = "time_range"
	// CapKeywordBM25: the keyword leg is real BM25 (with CJK segmentation), not a similarity
	// approximation. One backend substitutes trigram matching, which recalls noticeably worse — that
	// has to be visible when choosing between them.
	CapKeywordBM25 = "keyword_bm25"
	// CapFieldBoosts: keyword_query supports per-field boosting (title^3 and the like).
	CapFieldBoosts = "field_boosts"

	// CapWebhook: the platform relays webhooks to this plugin (RegisterWebhook was called).
	// The author never declares it — registering is the fact, and capabilitiesContract merges it in.
	// The credential row's webhook button and the plugin's webhook tab follow this bit.
	CapWebhook = "webhook"
)

// SetCapabilities declares which optional capabilities this plugin does and does not support.
//
// **List only what you know about.** A capability absent from the map is treated as undeclared, and
// the platform keeps its previous behaviour rather than inferring anything — an older plugin does not
// suddenly get judged as supporting nothing just because this mechanism exists.
func (p *Plugin) SetCapabilities(caps map[string]bool) { p.capabilities = caps }

// capabilitiesContract is what the registration handshake reports. A nil or empty map reports null,
// which the platform treats as undeclared.
func (p *Plugin) capabilitiesContract() map[string]bool {
	caps := p.capabilities
	// The webhook capability follows the fact, not a declaration: registering a handler is support.
	// Forgetting to declare it should never make the entry-point button disappear.
	if p.webhookFn != nil {
		merged := map[string]bool{CapWebhook: true}
		for k, v := range caps {
			merged[k] = v
		}
		caps = merged
	}
	if len(caps) == 0 {
		return nil
	}
	return caps
}
