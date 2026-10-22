// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// The README's quick start. It needs the compose Postgres, so it has no Output
// comment and go test only compiles it; CI runs the same program from
// examples/read-own-rows.
func ExampleTenantTx() {
	ctx := context.Background()
	db, err := demo.Setup(ctx) // two tenants' notes behind a row-level security policy
	if err != nil {
		log.Fatal(err)
	}
	err = tenantdb.TenantTx(ctx, db, demo.TenantA, func(tx *gorm.DB) error {
		var bodies []string
		if err := tx.Raw("SELECT body FROM notes ORDER BY body").Scan(&bodies).Error; err != nil {
			return err
		}
		fmt.Println(strings.Join(bodies, " | "))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}

// countNotes is a DAO method. It takes no tenant, because the scope in ctx picks one.
func countNotes(ctx context.Context, db *gorm.DB) (int64, error) {
	var n int64
	err := tenantdb.DB(ctx, db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
	return n, err
}

// One DAO method inside tenant B's scope, then outside any scope, where it
// fails with ErrNoScope. It needs a database in DATABASE_URL, so go test only
// compiles it. examples/dao-scope runs the same calls on the demo database.
func ExampleRunInScopeValue() {
	db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	const tenantB = "00000000-0000-4000-8000-00000000000b"
	n, err := tenantdb.RunInScopeValue(ctx, tenantB, tenantdb.ScopeEnforced,
		func(ctx context.Context) (int64, error) { return countNotes(ctx, db) })
	fmt.Println("in tenant B's scope:", n, err)
	_, err = countNotes(ctx, db)
	fmt.Println("outside any scope:", err)
}
