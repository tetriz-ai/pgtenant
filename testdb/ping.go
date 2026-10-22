// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testdb

import (
	"context"
	"fmt"

	"github.com/peterldowns/pgtestdb"
)

// Ping reports whether the Postgres server in conf is reachable, by connecting
// to conf.Database. Call it in TestMain before creating test databases, so an
// unreachable server can be skipped and a schema that fails to load still fails.
func Ping(ctx context.Context, conf pgtestdb.Config) error {
	db, err := conf.Connect()
	if err != nil {
		return fmt.Errorf("open %s: %w", conf.Database, err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping test server %s:%s: %w", conf.Host, conf.Port, err)
	}
	return nil
}
