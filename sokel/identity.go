// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"os"
	"strings"
)

// stableInstanceID is the replica's stable identity, reused across restarts.
//
// It used to be host-pid: a new pid on every restart made the platform see a brand-new replica, and
// the old rows piled up offline forever. In order of preference:
//  1. the SOKEL_INSTANCE_ID environment variable (explicit, for several replicas on one host);
//  2. a .sokel-instance-id.<token fingerprint> file in the working directory (first run generates
//     host-<4 random bytes> and writes it out). The token fingerprint is in the name because
//     several plugins or groups may share a directory — one shared file once made two plugins
//     register under the same instance id, and starting from another directory then produced a
//     pair of ghost replicas;
//  3. if writing fails (a read-only filesystem, say) fall back to host-pid: usable, but the
//     identity changes on restart.
const instanceIDFile = ".sokel-instance-id"

func stableInstanceID(token string) string {
	if v := strings.TrimSpace(pluginenv.Get("INSTANCE_ID")); v != "" {
		return v
	}
	file := instanceIDFile
	if token != "" {
		sum := sha256.Sum256([]byte(token))
		file = instanceIDFile + "." + hex.EncodeToString(sum[:4])
	}
	if b, err := os.ReadFile(file); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	host, _ := os.Hostname()
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	id := fmt.Sprintf("%s-%s", host, hex.EncodeToString(buf))
	if err := os.WriteFile(file, []byte(id+"\n"), 0o644); err != nil {
		return fmt.Sprintf("%s-%d", host, os.Getpid()) // fallback when the file cannot be written
	}
	return id
}
