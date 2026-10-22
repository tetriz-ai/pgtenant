// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package grpctenant_test

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	_ "github.com/tetriz-ai/pgtenant/gen/pgtenant/example/v1" // registers the annotated service
	"github.com/tetriz-ai/pgtenant/grpctenant"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// The interceptor takes tenantdb's functions in its config. Run it after the
// interceptor that authenticates the caller and calls grpctenant.WithOrgID.
// examples/grpc-service runs a full server.
func ExampleNewTenantScopeInterceptor() {
	cfg := grpctenant.TenantScopeConfig{
		Scopes: grpctenant.LoadTenantScopesFromRegistry(),
		NewContext: func(ctx context.Context, orgID string, scope int) context.Context {
			return tenantdb.NewContext(ctx, orgID, tenantdb.Scope(scope))
		},
		CommitOrRollback: tenantdb.CommitOrRollback,
	}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpctenant.NewTenantScopeInterceptor(cfg)))
	defer srv.Stop()
	fmt.Println("annotated methods:", len(cfg.Scopes))
	// Output: annotated methods: 3
}
