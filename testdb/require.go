// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testdb

import (
	"fmt"
	"os"
)

// RequireEnvVar is the environment switch that turns an unreachable test database
// from a skip into a failure. CI sets it; a developer without a local Postgres
// does not. Any value other than empty or "0" turns it on.
const RequireEnvVar = "PGTENANT_REQUIRE_TEST_DB"

// SkipOrFail reports an unreachable test database for the named suite.
//
// Under RequireEnvVar it exits 1 and does not return. Otherwise it prints why
// the suite is being skipped and returns, leaving the caller to skip on its own
// terms (a TestMain exits 0).
//
// Either way the message states that zero tests ran, so a skipped suite is
// readable as skipped in CI output rather than inferred from an exit code.
func SkipOrFail(suite string, err error) {
	if requireDB() {
		fmt.Fprintf(os.Stderr,
			"FAIL %s tests: 0 tests ran — %s is set but no test database is reachable: %v\n",
			suite, RequireEnvVar, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr,
		"SKIP %s tests: 0 tests ran — no test database reachable: %v\n"+
			"      (set %s=1 to make this a failure instead)\n",
		suite, err, RequireEnvVar)
}

// requireDB reads RequireEnvVar. Unset and "0" both mean "skipping is acceptable",
// so a script can opt out by setting it to 0 rather than having to unset it.
func requireDB() bool {
	v := os.Getenv(RequireEnvVar)
	return v != "" && v != "0"
}
