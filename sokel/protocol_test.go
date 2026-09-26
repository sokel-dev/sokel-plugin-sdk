// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"errors"
	"strings"
	"testing"
)

// The handshake used to carry no protocol version, so when the platform changed the protocol an
// older SDK just logged "registration failed … retrying" forever and never said "upgrade the SDK".
// Now the SDK reports its protocol and gives up with a readable message when the platform asks for more.
func TestRegisterBodyReportsProtocolAndSDK(t *testing.T) {
	p := New(Config{Token: "t"})
	body := p.registerBody("i", "h", nil)
	if body["protocol"] != wireProtocol {
		t.Errorf("registration must report protocol %d, got %v", wireProtocol, body["protocol"])
	}
	if sdk, _ := body["sdk"].(string); !strings.HasPrefix(sdk, "go/") {
		t.Errorf(`registration must report the SDK as "go/<version>", got %q`, sdk)
	}
}

func TestCheckProtocol(t *testing.T) {
	if err := checkProtocol(access{MinProtocol: wireProtocol}); err != nil {
		t.Errorf("a platform asking for our own protocol must be accepted: %v", err)
	}
	if err := checkProtocol(access{}); err != nil {
		t.Errorf("an older platform sends no min_protocol and must be accepted: %v", err)
	}
	err := checkProtocol(access{MinProtocol: wireProtocol + 1})
	if !errors.Is(err, ErrSDKTooOld) || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("a platform asking for a newer protocol must stop us with an upgrade hint, got %v", err)
	}
}
