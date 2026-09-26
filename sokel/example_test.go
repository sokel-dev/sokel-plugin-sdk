// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel_test

import (
	"encoding/json"
	"log"
	"os"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
)

// A plugin with one operation, registered by hand. Most plugins declare their contract in a manifest
// and use the generated OnXxx functions instead (`sokel-gen init`); this is what those expand to.
// The end-to-end version of this, run against a real broker, is in integration/.
func Example() {
	p := sokel.New(sokel.Config{
		Endpoint: os.Getenv("SOKEL_ENDPOINT"), // the platform's https:// address
		Token:    os.Getenv("SOKEL_TOKEN"),    // the access group's token
		Name:     "greeter",
	})
	p.Register(contract.Operation{
		ID: "greet", Label: "Greet",
		Inputs:  []contract.Field{{Name: "name", Label: "Name", Type: contract.ParamType("string")}},
		Outputs: []contract.Field{{Name: "greeting", Label: "Greeting", Type: contract.ParamType("string")}},
	}, func(ctx plugin.Ctx, raw json.RawMessage, out plugin.Sink) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return err
		}
		out.Vars(&struct {
			Greeting string `json:"greeting" sokel:"greeting"`
		}{"Hello, " + in.Name})
		return nil
	})
	if os.Getenv("SOKEL_TOKEN") == "" {
		return // no platform to connect to (this is what runs under go test)
	}
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
