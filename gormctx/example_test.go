// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package gormctx_test

import (
	"context"
	"fmt"
	"log"

	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/gormctx"
	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// countNotes is a DAO method that queries through tenantdb.DB.
func countNotes(ctx context.Context, db *gorm.DB) (int64, error) {
	var n int64
	err := tenantdb.DB(ctx, db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
	return n, err
}

// New lets a DAO method that queries through tenantdb.DB run inside the
// transaction of SystemTx. It needs the compose Postgres, so go test only
// compiles it.
func ExampleNew() {
	ctx := context.Background()
	db, err := demo.Setup(ctx)
	if err != nil {
		log.Fatal(err)
	}
	err = tenantdb.SystemTx(ctx, db, func(tx *gorm.DB) error {
		n, err := countNotes(gormctx.New(ctx, tx), db)
		if err != nil {
			return err
		}
		fmt.Println("notes across every tenant:", n)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
