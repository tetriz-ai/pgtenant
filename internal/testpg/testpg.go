// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package testpg holds the connection settings the repository's own tests and
// examples use for the compose Postgres.
package testpg

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/peterldowns/pgtestdb"

	// The pgx driver registers itself as "pgx" for database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/tetriz-ai/pgtenant/testdb"
)

// Config returns the settings for the compose Postgres on 127.0.0.1:5433.
// PGTENANT_TEST_HOST and PGTENANT_TEST_PORT override the host and port.
func Config() pgtestdb.Config {
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       env("PGTENANT_TEST_HOST", "localhost"),
		Port:       env("PGTENANT_TEST_PORT", "5433"),
		User:       "postgres",
		Password:   "password",
		Database:   "postgres",
		Options:    "sslmode=disable",
	}
}

// readyWait is how long Check waits for a server the run expects to accept
// connections, as demo.Setup does, so a run can start right after
// `docker compose up -d` or `podman-compose up -d`.
const readyWait = 30 * time.Second

// Check pings the server Config names, for a TestMain. It returns nil when the
// server answers. A run that expects a server, because it set
// PGTENANT_REQUIRE_TEST_DB, PGTENANT_TEST_HOST or PGTENANT_TEST_PORT, retries
// for up to 30 seconds first. When the server still does not answer, a run
// that set PGTENANT_TEST_HOST or PGTENANT_TEST_PORT exits 1, because it named a
// database it expected to test against. Any other run goes through
// testdb.SkipOrFail, and Check returns the ping error for the database tests to
// skip on.
func Check(suite string) error {
	return check(suite, readyWait)
}

func check(suite string, wait time.Duration) error {
	addressSet := os.Getenv("PGTENANT_TEST_HOST") != "" || os.Getenv("PGTENANT_TEST_PORT") != ""
	// The rule testdb.SkipOrFail applies to its switch.
	required := os.Getenv(testdb.RequireEnvVar) != "" && os.Getenv(testdb.RequireEnvVar) != "0"
	deadline := time.Now().Add(wait)
	err := testdb.Ping(context.Background(), Config())
	for err != nil && (addressSet || required) && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		err = testdb.Ping(context.Background(), Config())
	}
	if err == nil {
		return nil
	}
	if addressSet {
		fmt.Fprintf(os.Stderr,
			"FAIL %s tests: 0 tests ran — PGTENANT_TEST_HOST or PGTENANT_TEST_PORT is set but no test database is reachable there: %v\n",
			suite, err)
		os.Exit(1)
	}
	testdb.SkipOrFail(suite, err)
	return err
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
