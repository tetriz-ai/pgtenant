// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package pgtenant_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunExamplesIndexGate: scripts/run-examples.sh runs each example README's
// commands, so when the folders and examples/README.md disagree it stops
// before any of them runs.
func TestRunExamplesIndexGate(t *testing.T) {
	script, err := os.ReadFile("scripts/run-examples.sh")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		folders []string
		index   []string
	}{
		{"a folder the index does not list", []string{"listed", "unlisted"}, []string{"listed"}},
		{"an index entry with no folder", []string{"listed"}, []string{"listed", "missing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"go.mod":                  "module probe\n\ngo 1.26\n",
				"scripts/run-examples.sh": string(script),
				"examples/README.md":      indexOf(tt.index),
				// A package, so the runner's go vet has something to vet.
				"examples/listed/main.go": "package main\n\nfunc main() {}\n",
			}
			for _, f := range tt.folders {
				// The block leaves a file behind when it runs.
				files["examples/"+f+"/README.md"] = "```sh\ntouch ran-" + f + "\n```\n\n```text\nx\n```\n"
			}
			for name, body := range files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			out, err := exec.Command("sh", filepath.Join(root, "scripts/run-examples.sh")).CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("err = %v, want exit 1 (output: %s)", err, out)
			}
			if ran, _ := filepath.Glob(filepath.Join(root, "ran-*")); len(ran) > 0 {
				t.Fatalf("example blocks ran despite the index mismatch: %v (output: %s)", ran, out)
			}
			if !strings.Contains(string(out), "no example ran") {
				t.Errorf("output %q does not say that no example ran", out)
			}
		})
	}
}

// indexOf returns an examples/README.md that lists folders.
func indexOf(folders []string) string {
	var b strings.Builder
	b.WriteString("# Examples\n\n")
	for _, f := range folders {
		b.WriteString("- [" + f + "](" + f + "/)\n")
	}
	return b.String()
}
