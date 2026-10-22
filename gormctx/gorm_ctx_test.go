// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package gormctx_test

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/gormctx"
)

// dryRunDB returns a *gorm.DB that renders SQL without connecting.
func dryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 sslmode=disable"}), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	return db
}

// selectSQL renders a SELECT through db.
func selectSQL(db *gorm.DB) string {
	var ids []string
	return db.Table("notes").Select("id").Find(&ids).Statement.SQL.String()
}

func TestFromContextOrDefault(t *testing.T) {
	def := dryRunDB(t)
	carried := dryRunDB(t)
	tests := []struct {
		name       string
		ctx        context.Context
		wantPool   gorm.ConnPool
		wantLocked bool
	}{
		{"no value returns the default", context.Background(), def.Statement.ConnPool, false},
		{"a carried handle wins", gormctx.New(context.Background(), carried), carried.Statement.ConnPool, false},
		{"the lock key adds FOR UPDATE to the default", gormctx.NewWithLockKey(context.Background()), def.Statement.ConnPool, true},
		{"the lock key adds FOR UPDATE to a carried handle", gormctx.NewWithLockKey(gormctx.New(context.Background(), carried)), carried.Statement.ConnPool, true},
		{"NewWithLock carries a locked handle", gormctx.NewWithLock(context.Background(), def), def.Statement.ConnPool, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gormctx.FromContextOrDefault(tt.ctx, def)
			if got.Statement.ConnPool != tt.wantPool {
				t.Fatal("handle from the wrong source")
			}
			if got.Statement.Context != tt.ctx {
				t.Fatal("ctx not attached")
			}
			sql := selectSQL(got)
			if locked := strings.HasSuffix(sql, "FOR UPDATE"); locked != tt.wantLocked {
				t.Fatalf("SQL %q: locked = %v, want %v", sql, locked, tt.wantLocked)
			}
		})
	}
}

func TestFromContext(t *testing.T) {
	carried := dryRunDB(t)
	tests := []struct {
		name   string
		ctx    context.Context
		wantOK bool
	}{
		{"absent", context.Background(), false},
		{"present", gormctx.New(context.Background(), carried), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, ok := gormctx.FromContext(tt.ctx)
			if ok != tt.wantOK || (db != nil) != tt.wantOK {
				t.Fatalf("got (%v, %v), want ok=%v", db, ok, tt.wantOK)
			}
			ldb, lok := gormctx.FromContextWithLock(tt.ctx)
			if lok != tt.wantOK {
				t.Fatalf("FromContextWithLock ok = %v, want %v", lok, tt.wantOK)
			}
			if lok && !strings.HasSuffix(selectSQL(ldb), "FOR UPDATE") {
				t.Fatal("FromContextWithLock did not add FOR UPDATE")
			}
		})
	}
}

// TestFromContextOrDefaultNil returns nil, not a panic, when the context
// carries nothing and the default is nil.
func TestFromContextOrDefaultNil(t *testing.T) {
	if got := gormctx.FromContextOrDefault(gormctx.NewWithLockKey(context.Background()), nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestNilHandles: a nil db is never stored, so nothing downstream gets a nil
// handle or panics.
func TestNilHandles(t *testing.T) {
	def := dryRunDB(t)
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"New with nil", gormctx.New(context.Background(), nil)},
		{"NewWithLock with nothing to lock", gormctx.NewWithLock(context.Background(), nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := gormctx.FromContext(tt.ctx); ok {
				t.Fatal("FromContext reports a stored handle")
			}
			if db, ok := gormctx.FromContextWithLock(tt.ctx); ok || db != nil {
				t.Fatal("FromContextWithLock returned a handle")
			}
			if got := gormctx.FromContextOrDefault(tt.ctx, def); got == nil || got.Statement.ConnPool != def.Statement.ConnPool {
				t.Fatal("FromContextOrDefault did not fall back to the default")
			}
		})
	}
}
