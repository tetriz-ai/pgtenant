// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testdb

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRequireDB pins the truth table of the gate switch. An inverted condition
// here would silently defeat the gate — a run that demanded a database would go
// back to skipping — and no caller's test would notice, because every caller
// only ever exercises whichever branch its own environment happens to take.
func TestRequireDB(t *testing.T) {
	tests := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{name: "unset means skipping is acceptable", set: false, want: false},
		{name: "empty means skipping is acceptable", set: true, value: "", want: false},
		{name: "explicit 0 opts out", set: true, value: "0", want: false},
		{name: "1 requires a database", set: true, value: "1", want: true},
		{name: "any other value requires a database", set: true, value: "true", want: true},
		{name: "00 is not the opt-out spelling", set: true, value: "00", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(RequireEnvVar, tt.value)
			} else {
				// t.Setenv restores the prior value at cleanup, so clearing it
				// here is scoped to this subtest.
				t.Setenv(RequireEnvVar, "")
				if err := os.Unsetenv(RequireEnvVar); err != nil {
					t.Fatalf("unset %s: %v", RequireEnvVar, err)
				}
			}
			if got := requireDB(); got != tt.want {
				t.Errorf("requireDB() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSkipOrFail covers the two exit paths, which need a subprocess because
// SkipOrFail calls os.Exit. The exit code is the whole contract: a gate reads it
// and nothing else, so "skip returns and the caller exits 0" versus "fail exits 1"
// is what distinguishes a suite that skipped from one that passed.
func TestSkipOrFail(t *testing.T) {
	tests := []struct {
		name         string
		requireValue string
		wantExit     int
		wantStderr   string
	}{
		{name: "skips when the run did not demand a database", requireValue: "0", wantExit: 0, wantStderr: "SKIP demo tests: 0 tests ran"},
		{name: "fails the run when it demanded a database", requireValue: "1", wantExit: 1, wantStderr: "FAIL demo tests: 0 tests ran"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Re-exec this test binary into the helper below; see TestHelperSkipOrFail.
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperSkipOrFail$")
			cmd.Env = append(os.Environ(),
				"PGTENANT_TESTDB_HELPER=1",
				RequireEnvVar+"="+tt.requireValue,
			)
			out, err := cmd.CombinedOutput()

			exit := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exit = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("running helper: %v (output: %s)", err, out)
			}

			if exit != tt.wantExit {
				t.Errorf("helper exit = %d, want %d (output: %s)", exit, tt.wantExit, out)
			}
			if !strings.Contains(string(out), tt.wantStderr) {
				t.Errorf("helper output %q does not contain %q", out, tt.wantStderr)
			}
			// Both paths must say zero tests ran, so a reader of CI output can
			// tell a skipped suite from a passing one without the exit code.
			if !strings.Contains(string(out), "0 tests ran") {
				t.Errorf("helper output %q does not announce that zero tests ran", out)
			}
		})
	}
}

// TestHelperSkipOrFail is the subprocess body for TestSkipOrFail. It is inert
// unless PGTENANT_TESTDB_HELPER is set, so a normal run of this package skips it.
func TestHelperSkipOrFail(t *testing.T) {
	if os.Getenv("PGTENANT_TESTDB_HELPER") != "1" {
		t.Skip("helper process only; driven by TestSkipOrFail")
	}
	SkipOrFail("demo", errors.New("planted unreachable-database error"))
	// Reached only on the skip path — SkipOrFail returns rather than exiting, and
	// the caller is the one that exits 0. Mirror that here so the subprocess's
	// exit code reflects the skip path rather than this test's own result.
	os.Exit(0)
}
