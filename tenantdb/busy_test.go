// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// TestScopeBusy: a scope's transaction runs one statement at a time. A
// statement that starts while another is running, or while its rows are still
// open, fails with ErrScopeBusy instead of reaching the connection.
func TestScopeBusy(t *testing.T) {
	db := newGormDB(t, forceRLS)
	seed(t, db, orgA, orgB)

	// openRows starts a query in ctx's scope and leaves its rows open.
	openRows := func(t *testing.T, ctx context.Context) *sql.Rows {
		t.Helper()
		rows, err := tenantdb.DB(ctx, db).Raw("SELECT body FROM notes").Rows()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rows.Close() })
		return rows
	}
	count := func(ctx context.Context) error {
		var n int64
		return tenantdb.DB(ctx, db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
	}
	tests := []struct {
		name string
		run  func(t *testing.T, ctx context.Context) error
		want error
	}{
		{"a query while another query's rows are open", func(t *testing.T, ctx context.Context) error {
			openRows(t, ctx)
			return count(ctx)
		}, tenantdb.ErrScopeBusy},
		{"an Exec while another query's rows are open", func(t *testing.T, ctx context.Context) error {
			openRows(t, ctx)
			return tenantdb.DB(ctx, db).Exec("SELECT 1").Error
		}, tenantdb.ErrScopeBusy},
		{"a Row while another query's rows are open", func(t *testing.T, ctx context.Context) error {
			openRows(t, ctx)
			return tenantdb.DB(ctx, db).Raw("SELECT 1").Row().Err()
		}, tenantdb.ErrScopeBusy},
		{"a nested transaction in the scope", func(_ *testing.T, ctx context.Context) error {
			return tenantdb.DB(ctx, db).Transaction(func(tx *gorm.DB) error {
				var n int64
				return tx.Raw("SELECT count(*) FROM notes").Scan(&n).Error
			})
		}, nil},
		{"a query after the rows are closed", func(t *testing.T, ctx context.Context) error {
			_ = openRows(t, ctx).Close()
			return count(ctx)
		}, nil},
		{"a query after the rows are read to the end", func(t *testing.T, ctx context.Context) error {
			rows := openRows(t, ctx)
			for rows.Next() {
				var body string
				if err := rows.Scan(&body); err != nil {
					t.Fatal(err)
				}
			}
			return count(ctx)
		}, nil},
		{"one query after another", func(_ *testing.T, ctx context.Context) error {
			if err := count(ctx); err != nil {
				return err
			}
			return count(ctx)
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(t, scoped(t, orgA)); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("goroutines sharing one scope", func(t *testing.T) {
		ctx := scoped(t, orgA)
		var wg sync.WaitGroup
		errs := make(chan error, 8*50)
		for range 8 {
			wg.Go(func() {
				for range 50 {
					var n int64
					err := tenantdb.DB(ctx, db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
					if err == nil && n != 1 {
						err = errors.New("tenant A saw a count other than its one note")
					}
					errs <- err
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, tenantdb.ErrScopeBusy) {
				t.Fatalf("err = %v, want nil or ErrScopeBusy", err)
			}
		}
	})
}
