// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"errors"
	"fmt"
	"runtime/debug"
)

// wireProtocol is the plugin wire protocol version this SDK speaks. It only goes up for changes both
// sides must make together:
//
//	1 = the original protocol (replies on the global _INBOX.)
//	2 = per-group reply prefix (inbox_prefix, v0.5.5)
//
// The number is reported at registration, and the platform's min_protocol (sent with the access
// credentials) is checked before connecting.
const wireProtocol = 2

// ErrSDKTooOld means the platform requires a newer wire protocol than this SDK speaks. Run returns it
// instead of retrying: no amount of waiting fixes it, only rebuilding the plugin with a newer SDK does.
var ErrSDKTooOld = errors.New("sokel: the platform requires a newer plugin SDK")

// checkProtocol compares the platform's minimum with ours. A platform that sends no min_protocol
// predates the negotiation and accepts whatever connects.
func checkProtocol(acc access) error {
	if acc.MinProtocol > wireProtocol {
		return fmt.Errorf("%w: it needs wire protocol %d, this SDK (%s) speaks %d — upgrade github.com/sokel-dev/sokel-plugin-sdk and rebuild",
			ErrSDKTooOld, acc.MinProtocol, sdkIdent(), wireProtocol)
	}
	return nil
}

// sdkIdent is "go/<module version>", reported at registration so the platform can tell which SDK a
// replica runs. The version comes from the build info of the plugin binary; a plugin built inside
// this repository reports "go/(devel)".
func sdkIdent() string {
	const mod = "github.com/sokel-dev/sokel-plugin-sdk"
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Path == mod {
			return "go/" + bi.Main.Version
		}
		for _, d := range bi.Deps {
			if d.Path == mod {
				if d.Replace != nil && d.Replace.Version != "" {
					return "go/" + d.Replace.Version
				}
				return "go/" + d.Version
			}
		}
	}
	return "go/(devel)"
}
