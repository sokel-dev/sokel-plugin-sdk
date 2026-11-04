// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package gen

import (
	"strings"
	"testing"
)

func TestBrokerNetworkIsReservedDeploymentEnvironment(t *testing.T) {
	_, err := ParseManifest([]byte(`plugin: {name: demo}
operations: [{id: ping, label: Ping, inputs: [], outputs: []}]
deployment:
  targets: [{kind: container, ref: example/demo:v1}]
  env: [{name: SOKEL_BROKER_NETWORK}]
`), false)
	if err == nil || !strings.Contains(err.Error(), "SOKEL_BROKER_NETWORK") {
		t.Fatalf("the platform connection selector must be reserved, got %v", err)
	}
}
