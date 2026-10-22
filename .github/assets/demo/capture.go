//go:build ignore

// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Command capture uses only the disposable example schema.
package main

import (
	"context"
	"fmt"
	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
	"strings"
)

func main() {
	ctx := context.Background()
	db, err := demo.Setup(ctx)
	if err != nil {
		panic(err)
	}
	for _, t := range []struct{ name, id string }{{"A", demo.TenantA}, {"B", demo.TenantB}} {
		err = tenantdb.RunInScope(ctx, t.id, tenantdb.ScopeEnforced, func(ctx context.Context) error {
			var rows []string
			if err := tenantdb.DB(ctx, db).Raw("SELECT body FROM notes ORDER BY body").Scan(&rows).Error; err != nil {
				return err
			}
			fmt.Println(t.name + ": " + strings.Join(rows, " | "))
			return nil
		})
		if err != nil {
			panic(err)
		}
	}
	var n int64
	err = tenantdb.DB(ctx, db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
	fmt.Println("outside scope:", err)
}
