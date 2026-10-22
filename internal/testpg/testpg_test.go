// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testpg_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tetriz-ai/pgtenant/internal/testpg"
	"github.com/tetriz-ai/pgtenant/testdb"
)

// helperEnv makes this test binary act as a TestMain that calls Check, or
// CheckWithin when helperWaitEnv holds a duration.
const (
	helperEnv     = "PGTENANT_TESTPG_HELPER"
	helperWaitEnv = "PGTENANT_TESTPG_HELPER_WAIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		if wait, err := time.ParseDuration(os.Getenv(helperWaitEnv)); err == nil {
			_ = testpg.CheckWithin("helper", wait)
		} else {
			_ = testpg.Check("helper")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestCheck pins that a run which expects a database fails when nothing
// answers, even without PGTENANT_REQUIRE_TEST_DB when it names an address, and
// that it waits for a server that is still starting, as one is right after
// `docker compose up -d`. Port 1 has no Postgres.
func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		env      func(t *testing.T) []string
		wantFail bool
	}{
		{name: "port set", wantFail: true, env: func(*testing.T) []string {
			return []string{"PGTENANT_TEST_PORT=1", helperWaitEnv + "=1s"}
		}},
		{name: "host and port set", wantFail: true, env: func(*testing.T) []string {
			return []string{"PGTENANT_TEST_HOST=127.0.0.1", "PGTENANT_TEST_PORT=1", helperWaitEnv + "=1s"}
		}},
		{name: "a required server that starts late", env: func(t *testing.T) []string {
			return []string{"PGTENANT_REQUIRE_TEST_DB=1", "PGTENANT_TEST_HOST=127.0.0.1", "PGTENANT_TEST_PORT=" + lateServer(t, 2*time.Second)}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), helperEnv+"=1", helperWaitEnv+"=", "PGTENANT_REQUIRE_TEST_DB=0", "PGTENANT_TEST_HOST=", "PGTENANT_TEST_PORT=")
			cmd.Env = append(cmd.Env, tt.env(t)...)
			out, err := cmd.CombinedOutput()
			reported := strings.Contains(string(out), "FAIL helper tests: 0 tests ran")
			if !tt.wantFail {
				if err != nil || reported {
					t.Fatalf("helper err = %v, want success (output: %s)", err, out)
				}
				return
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("helper err = %v, want exit 1 (output: %s)", err, out)
			}
			if !reported {
				t.Errorf("helper output %q does not report the failure", out)
			}
		})
	}
}

// lateServer returns a local port that drops connections for delay, as a
// server that is still starting does, then forwards them to the test Postgres.
// It skips the test when that Postgres is not reachable.
func lateServer(t *testing.T, delay time.Duration) string {
	t.Helper()
	conf := testpg.Config()
	if err := testdb.Ping(context.Background(), conf); err != nil {
		t.Skipf("no test database to forward to: %v", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	ready := time.Now().Add(delay)
	go func() {
		for {
			client, err := l.Accept()
			if err != nil {
				return
			}
			if time.Now().Before(ready) {
				_ = client.Close()
				continue
			}
			go forward(client, net.JoinHostPort(conf.Host, conf.Port))
		}
	}()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// forward copies bytes between client and a new connection to target.
func forward(client net.Conn, target string) {
	defer func() { _ = client.Close() }()
	server, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = server.Close() }()
	go func() { _, _ = io.Copy(server, client) }()
	_, _ = io.Copy(client, server)
}
