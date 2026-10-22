// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package grpctenant_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	pgtenantv1 "github.com/tetriz-ai/pgtenant/gen/pgtenant/v1"
	"github.com/tetriz-ai/pgtenant/grpctenant"
	"github.com/tetriz-ai/pgtenant/internal/testpg"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// TestRollbackFailureLoggedWithTenantdb: wired to tenantdb's own functions, a
// rollback that fails after a panicking handler reaches the Logger. The
// handler ends its own database session, so the rollback cannot reach the
// server. A system scope is used because the compose role is a superuser.
func TestRollbackFailureLoggedWithTenantdb(t *testing.T) {
	if err := testpg.Check("grpctenant"); err != nil {
		t.Skipf("no test database: %v", err)
	}
	db, err := gorm.Open(postgres.Open(testpg.Config().URL()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	own := &records{}
	cfg := grpctenant.TenantScopeConfig{
		Scopes: grpctenant.TenantScopeMap{countAll: pgtenantv1.TenantScope_TENANT_SCOPE_SYSTEM},
		NewContext: func(ctx context.Context, orgID string, scope int) context.Context {
			return tenantdb.NewContext(ctx, orgID, tenantdb.Scope(scope))
		},
		CommitOrRollback: tenantdb.CommitOrRollback,
		Logger:           slog.New(own),
	}
	func() {
		defer func() {
			if r := recover(); r != "handler bug" {
				t.Fatalf("recovered %v, want the handler's own panic", r)
			}
		}()
		_, _ = grpctenant.NewTenantScopeInterceptor(cfg)(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: countAll},
			func(ctx context.Context, _ any) (any, error) {
				_ = tenantdb.DB(ctx, db).Exec("SELECT pg_terminate_backend(pg_backend_pid())").Error
				panic("handler bug")
			})
	}()
	got := own.all()
	if len(got) != 1 {
		t.Fatalf("Logger got %d records, want 1", len(got))
	}
	if msg, _ := attrs(got[0])["error"].(error); msg == nil || !strings.Contains(msg.Error(), "rollback failed") {
		t.Fatalf("error attribute = %v, want tenantdb's rollback failure", attrs(got[0])["error"])
	}
}
