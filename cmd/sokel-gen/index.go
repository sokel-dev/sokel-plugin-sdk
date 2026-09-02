package main

// Building a registry index: walk plugins/<org>/<name>/, validate every manifest, write index.json.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

func runIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	out := fs.String("o", "", "write to this file instead of stdout")
	check := fs.Bool("check", false, "verify the written index is up to date, changing nothing (for CI)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	idx, err := sokelgen.BuildIndex(root)
	if err != nil {
		return err
	}
	b, err := sokelgen.RenderIndex(idx)
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Print(string(b))
		return nil
	}
	if *check {
		cur, rerr := os.ReadFile(*out)
		if rerr != nil {
			return fmt.Errorf("%s is missing; run sokel-gen index -o %s %s", *out, *out, root)
		}
		if string(cur) != string(b) {
			return fmt.Errorf("%s is stale (an entry changed and the index was not rebuilt); "+
				"run: sokel-gen index -o %s %s", *out, *out, root)
		}
		fmt.Printf("sokel-gen: %s is up to date (%d plugins)\n", *out, len(idx.Plugins))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("sokel-gen: wrote %s (%d plugins)\n", *out, len(idx.Plugins))
	return nil
}
