// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package grpctenant provides gRPC interceptors that read the
// pgtenant.v1.tenant_scope method option and put each RPC in the matching
// tenant scope.
package grpctenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	pgtenantv1 "github.com/tetriz-ai/pgtenant/gen/pgtenant/v1"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// TenantScopeMap maps gRPC full method names to their tenant scope.
type TenantScopeMap map[string]pgtenantv1.TenantScope

// LoadTenantScopesFromRegistry reads tenant_scope method options from all proto
// file descriptors registered in the global proto registry.
func LoadTenantScopesFromRegistry() TenantScopeMap {
	scopes := make(TenantScopeMap)

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				md := sd.Methods().Get(j)
				opts, ok := md.Options().(*descriptorpb.MethodOptions)
				if !ok || opts == nil {
					continue
				}
				if !proto.HasExtension(opts, pgtenantv1.E_TenantScope) {
					continue
				}
				scope, ok := proto.GetExtension(opts, pgtenantv1.E_TenantScope).(pgtenantv1.TenantScope)
				if !ok {
					continue
				}
				methodName := fmt.Sprintf("/%s/%s", sd.FullName(), md.Name())
				scopes[methodName] = scope
			}
		}
		return true
	})

	return scopes
}

// TenantContextFunc creates a new context with tenant metadata for lazy
// transaction creation. scope is int(tenantdb.ScopeEnforced) or
// int(tenantdb.ScopeSystem).
type TenantContextFunc func(ctx context.Context, orgID string, scope int) context.Context

// CommitOrRollbackFunc finalizes the lazy transaction if one was created.
// Return err itself when the rollback succeeds or nothing was open; any other
// non-nil value is treated as a failed rollback and logged.
type CommitOrRollbackFunc func(ctx context.Context, err error) error

// TenantScopeConfig holds the dependencies for the tenant scope interceptor.
// NewContext gets tenantdb's scope values, and tenantdb.SystemTenantID as the
// org of a system scope.
//
// By default the interceptors take the org only from the context, where an
// authenticating interceptor that runs first puts it with [WithOrgID]. The two
// Trust fields widen that to values the caller sends; an org in the context
// always wins over both.
type TenantScopeConfig struct {
	// Scopes maps full method names to their tenant_scope. Use
	// [LoadTenantScopesFromRegistry] to read it from the compiled protos.
	Scopes TenantScopeMap
	// NewContext opens a lazy scope for orgID and scope, for example
	// tenantdb.NewContext.
	NewContext TenantContextFunc
	// CommitOrRollback finishes that scope after the handler returns, for
	// example tenantdb.CommitOrRollback.
	CommitOrRollback CommitOrRollbackFunc
	// TrustIncomingMetadata reads org_id from incoming gRPC metadata when the
	// context has no org. Turn it on only when every caller is a trusted
	// service passing the org on; otherwise any client picks its tenant.
	TrustIncomingMetadata bool
	// TrustRequestOrgID reads organization_id from the request, through
	// [TenantScoped], for unary TENANT_SCOPE_CALLER_MANAGED RPCs when the
	// context has no org. Turn it on only when the request comes from a trusted
	// producer, such as a queue consumer; otherwise any client picks its tenant.
	TrustRequestOrgID bool
	// Logger receives an Error record, with the error and the method, when the
	// rollback after a handler that panicked or called runtime.Goexit fails:
	// CommitOrRollback returned a non-nil value other than the err it was given.
	// Nil logs nothing.
	Logger *slog.Logger
}

// orgID returns the org the config trusts: the context's, then, only with
// TrustIncomingMetadata, the first incoming org_id value.
func (cfg TenantScopeConfig) orgID(ctx context.Context) string {
	if orgID := OrgIDFromContext(ctx); orgID != "" {
		return orgID
	}
	if cfg.TrustIncomingMetadata {
		return OrgIDFromContextOrMetadata(ctx)
	}
	return ""
}

// NewTenantScopeInterceptor returns a gRPC unary interceptor that enforces
// tenant isolation using proto-defined tenant_scope method options.
//
// An RPC with no tenant_scope option is not intercepted. ENFORCED needs an org
// (see [TenantScopeConfig]): it answers FailedPrecondition without one and
// InvalidArgument when the org is not a UUID, puts the canonical org in the
// handler's context and outgoing metadata, opens an enforced scope, and commits
// or rolls back after the handler. SYSTEM opens a system scope. CALLER_MANAGED
// puts the canonical org in the context and outgoing metadata and opens no
// transaction. A handler that panics or calls runtime.Goexit is rolled back.
//
// It panics, naming the field, when Scopes is nil, or when Scopes has an
// ENFORCED or SYSTEM method and cfg lacks NewContext or CommitOrRollback. A
// misconfigured server then fails at startup, not on its first request.
func NewTenantScopeInterceptor(cfg TenantScopeConfig) grpc.UnaryServerInterceptor {
	cfg.mustServeScopes()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		scope, exists := cfg.Scopes[info.FullMethod]
		if !exists {
			return handler(ctx, req)
		}
		var resp any
		run, err := cfg.prepare(ctx, scope, req, info.FullMethod)
		if err != nil {
			return nil, err
		}
		err = run(func(ctx context.Context) error {
			var herr error
			resp, herr = handler(ctx, req)
			return herr
		})
		return resp, err
	}
}

// NewTenantScopeStreamInterceptor returns the stream form of
// NewTenantScopeInterceptor, with the same rules and the same panic on an
// incomplete cfg. A stream has no request message, so TrustRequestOrgID does
// not apply.
func NewTenantScopeStreamInterceptor(cfg TenantScopeConfig) grpc.StreamServerInterceptor {
	cfg.mustServeScopes()
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		scope, exists := cfg.Scopes[info.FullMethod]
		if !exists {
			return handler(srv, ss)
		}
		run, err := cfg.prepare(ss.Context(), scope, nil, info.FullMethod)
		if err != nil {
			return err
		}
		return run(func(ctx context.Context) error {
			return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
		})
	}
}

// mustServeScopes panics, naming the field, when Scopes is nil, so no RPC
// would be scoped, or when cfg cannot serve a method in Scopes. Any scope but
// CALLER_MANAGED opens one, as prepare does.
func (cfg TenantScopeConfig) mustServeScopes() {
	if cfg.Scopes == nil {
		panic("grpctenant: TenantScopeConfig.Scopes is nil, so no RPC would be scoped")
	}
	opens := false
	for _, scope := range cfg.Scopes {
		if scope != pgtenantv1.TenantScope_TENANT_SCOPE_CALLER_MANAGED {
			opens = true
		}
	}
	switch {
	case opens && cfg.NewContext == nil:
		panic("grpctenant: TenantScopeConfig.NewContext is nil, but Scopes has a method that opens a scope")
	case opens && cfg.CommitOrRollback == nil:
		panic("grpctenant: TenantScopeConfig.CommitOrRollback is nil, but Scopes has a method that opens a scope")
	}
}

// errHandlerDidNotReturn is the error a scope is rolled back with when the
// handler panics or calls runtime.Goexit.
var errHandlerDidNotReturn = errors.New("grpctenant: handler did not return")

// prepare resolves the org for scope and returns a function that runs the
// handler in the right scope, with the context the handler gets.
func (cfg TenantScopeConfig) prepare(ctx context.Context, scope pgtenantv1.TenantScope, req any, method string) (func(func(context.Context) error) error, error) {
	switch scope {
	case pgtenantv1.TenantScope_TENANT_SCOPE_SYSTEM:
		ctx = cfg.NewContext(ctx, tenantdb.SystemTenantID, int(tenantdb.ScopeSystem))
		return cfg.inScope(ctx, method), nil

	case pgtenantv1.TenantScope_TENANT_SCOPE_CALLER_MANAGED:
		orgID := cfg.orgID(ctx)
		if orgID == "" && cfg.TrustRequestOrgID {
			if ts, ok := req.(TenantScoped); ok {
				orgID = ts.GetOrganizationId()
			}
		}
		if orgID != "" {
			canonical, err := canonicalOrgID(orgID, method)
			if err != nil {
				return nil, err
			}
			ctx = withOrgID(ctx, canonical)
			ctx = metadata.AppendToOutgoingContext(ctx, "org_id", canonical)
		}
		return func(run func(context.Context) error) error { return run(ctx) }, nil

	default: // TENANT_SCOPE_ENFORCED
		orgID := cfg.orgID(ctx)
		if orgID == "" {
			return nil, status.Errorf(codes.FailedPrecondition, "tenant context required for %s", method)
		}
		canonical, err := canonicalOrgID(orgID, method)
		if err != nil {
			return nil, err
		}
		ctx = withOrgID(ctx, canonical)
		ctx = metadata.AppendToOutgoingContext(ctx, "org_id", canonical)
		ctx = cfg.NewContext(ctx, canonical, int(tenantdb.ScopeEnforced))
		return cfg.inScope(ctx, method), nil
	}
}

// inScope runs the handler in the scope ctx carries, then commits or rolls
// back. A handler that does not return is rolled back.
func (cfg TenantScopeConfig) inScope(ctx context.Context, method string) func(func(context.Context) error) error {
	return func(run func(context.Context) error) error {
		returned := false
		defer func() {
			if returned {
				return
			}
			// CommitOrRollback hands the error back unchanged when the rollback
			// succeeds or nothing was open, and wraps it when the rollback
			// fails, so only the identical value means success.
			if err := cfg.CommitOrRollback(ctx, errHandlerDidNotReturn); err != nil && err != errHandlerDidNotReturn && cfg.Logger != nil {
				cfg.Logger.ErrorContext(ctx, "tenant scope: rollback after an unfinished handler failed",
					slog.Any("error", err), slog.String("method", method))
			}
		}()
		err := run(ctx)
		returned = true
		return cfg.CommitOrRollback(ctx, err)
	}
}

// canonicalOrgID returns orgID as a canonical UUID, or InvalidArgument.
func canonicalOrgID(orgID, method string) (string, error) {
	parsed, err := uuid.Parse(orgID)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "org_id for %s is not a UUID", method)
	}
	return parsed.String(), nil
}

// OrgIDFromContextOrMetadata reads org_id from ctx first, then falls
// back to incoming gRPC metadata for service-to-service calls where
// org_id arrives via metadata (ctx values don't cross gRPC boundaries).
// The interceptors call it only when TenantScopeConfig.TrustIncomingMetadata
// is on, because metadata comes from the caller.
func OrgIDFromContextOrMetadata(ctx context.Context) string {
	if orgID := OrgIDFromContext(ctx); orgID != "" {
		return orgID
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("org_id"); len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}
