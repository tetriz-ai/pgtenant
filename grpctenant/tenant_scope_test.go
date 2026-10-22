// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package grpctenant_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	examplev1 "github.com/tetriz-ai/pgtenant/gen/pgtenant/example/v1"
	pgtenantv1 "github.com/tetriz-ai/pgtenant/gen/pgtenant/v1"
	"github.com/tetriz-ai/pgtenant/grpctenant"
)

const (
	listNotes = "/pgtenant.example.v1.NoteService/ListNotes"
	countAll  = "/pgtenant.example.v1.NoteService/CountAllNotes"
	addNote   = "/pgtenant.example.v1.NoteService/AddNote"
	ping      = "/pgtenant.example.v1.NoteService/Ping"
	unknown   = "/pgtenant.example.v1.NoteService/Unknown" // mapped to a scope value the enum does not define
	orgA      = "00000000-0000-4000-8000-00000000000a"
	sentinel  = "__system__"
)

// recorder is a fake transaction layer: it records what the interceptor asked
// for and returns commitErr from CommitOrRollback.
type recorder struct {
	newContext     []string // "orgID/scope" per NewContext call
	finished       []error  // err passed to each CommitOrRollback call
	finishedHolder []any    // the scope holder in each CommitOrRollback call's ctx
	commitErr      error
}

type holderKey struct{}

func (r *recorder) config() grpctenant.TenantScopeConfig {
	return grpctenant.TenantScopeConfig{
		Scopes: grpctenant.TenantScopeMap{
			listNotes: pgtenantv1.TenantScope_TENANT_SCOPE_ENFORCED,
			countAll:  pgtenantv1.TenantScope_TENANT_SCOPE_SYSTEM,
			addNote:   pgtenantv1.TenantScope_TENANT_SCOPE_CALLER_MANAGED,
			unknown:   pgtenantv1.TenantScope(99),
		},
		NewContext: func(ctx context.Context, orgID string, scope int) context.Context {
			r.newContext = append(r.newContext, orgID+"/"+string(rune('0'+scope)))
			return context.WithValue(ctx, holderKey{}, orgID)
		},
		CommitOrRollback: func(ctx context.Context, err error) error {
			r.finished = append(r.finished, err)
			r.finishedHolder = append(r.finishedHolder, ctx.Value(holderKey{}))
			if r.commitErr != nil {
				return r.commitErr
			}
			return err
		},
	}
}

// seen is what the handler observed.
type seen struct {
	called   bool
	holder   any
	orgID    string
	outgoing []string
}

func observe(s *seen) func(ctx context.Context) {
	return func(ctx context.Context) {
		s.called = true
		s.holder = ctx.Value(holderKey{})
		s.orgID = grpctenant.OrgIDFromContext(ctx)
		md, _ := metadata.FromOutgoingContext(ctx)
		s.outgoing = md.Get("org_id")
	}
}

// interceptorCase is one call through the unary and, unless unaryOnly, the
// stream interceptor.
type interceptorCase struct {
	name           string
	method         string
	ctx            context.Context
	req            any
	trustMetadata  bool
	trustRequest   bool
	unaryOnly      bool // the request body exists only for unary calls
	handlerErr     error
	commitErr      error
	wantCode       codes.Code
	wantErr        error
	wantCalled     bool
	wantNewContext []string
	wantFinished   []error
	wantOrgID      string
	wantOutgoing   []string
}

var (
	errHandler = errors.New("handler failed")
	errCommit  = errors.New("commit failed")
)

const orgB = "00000000-0000-4000-8000-00000000000b"

func incoming(org string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("org_id", org))
}

func verified(org string) context.Context {
	return grpctenant.WithOrgID(context.Background(), org)
}

// verifiedAndIncoming carries a verified org and a different client-sent one.
func verifiedAndIncoming(org, sent string) context.Context {
	return metadata.NewIncomingContext(grpctenant.WithOrgID(context.Background(), org), metadata.Pairs("org_id", sent))
}

func interceptorCases() []interceptorCase {
	return []interceptorCase{
		{
			name: "an unannotated method passes through untouched", method: ping,
			ctx: context.Background(), wantCalled: true,
		},
		{
			name: "enforced opens a scope for the verified org and commits", method: listNotes,
			ctx: verified(orgA), wantCalled: true,
			wantNewContext: []string{orgA + "/0"}, wantFinished: []error{nil},
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "enforced without an org is FailedPrecondition and never runs the handler", method: listNotes,
			ctx: context.Background(), wantCode: codes.FailedPrecondition,
		},
		{
			name: "enforced ignores a client-sent org_id by default", method: listNotes,
			ctx: incoming(orgB), wantCode: codes.FailedPrecondition,
		},
		{
			name: "enforced accepts a client-sent org_id with TrustIncomingMetadata", method: listNotes,
			ctx: incoming(orgB), trustMetadata: true, wantCalled: true,
			wantNewContext: []string{orgB + "/0"}, wantFinished: []error{nil}, wantOrgID: orgB, wantOutgoing: []string{orgB},
		},
		{
			name: "enforced prefers the verified org over trusted metadata", method: listNotes,
			ctx: verifiedAndIncoming(orgA, orgB), trustMetadata: true, wantCalled: true,
			wantNewContext: []string{orgA + "/0"}, wantFinished: []error{nil},
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "enforced passes the handler error to CommitOrRollback", method: listNotes,
			ctx: verified(orgA), handlerErr: errHandler, wantErr: errHandler, wantCalled: true,
			wantNewContext: []string{orgA + "/0"}, wantFinished: []error{errHandler},
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "enforced returns the commit error", method: listNotes,
			ctx: verified(orgA), commitErr: errCommit, wantErr: errCommit, wantCalled: true,
			wantNewContext: []string{orgA + "/0"}, wantFinished: []error{nil},
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "system opens a sentinel scope without an org", method: countAll,
			ctx: context.Background(), wantCalled: true,
			wantNewContext: []string{sentinel + "/1"}, wantFinished: []error{nil},
		},
		{
			name: "system returns the commit error", method: countAll,
			ctx: context.Background(), commitErr: errCommit, wantErr: errCommit, wantCalled: true,
			wantNewContext: []string{sentinel + "/1"}, wantFinished: []error{nil},
		},
		{
			name: "caller-managed sets the verified org and opens no scope", method: addNote,
			ctx: verified(orgA), wantCalled: true,
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "caller-managed ignores a client-sent org_id by default", method: addNote,
			ctx: incoming(orgB), wantCalled: true,
		},
		{
			name: "caller-managed accepts a client-sent org_id with TrustIncomingMetadata", method: addNote,
			ctx: incoming(orgB), trustMetadata: true, wantCalled: true,
			wantOrgID: orgB, wantOutgoing: []string{orgB},
		},
		{
			name: "caller-managed ignores the request's organization_id by default", method: addNote, unaryOnly: true,
			ctx: context.Background(), req: &examplev1.AddNoteRequest{OrganizationId: orgB}, wantCalled: true,
		},
		{
			name: "caller-managed accepts the request's organization_id with TrustRequestOrgID", method: addNote, unaryOnly: true,
			ctx: context.Background(), req: &examplev1.AddNoteRequest{OrganizationId: orgB}, trustRequest: true, wantCalled: true,
			wantOrgID: orgB, wantOutgoing: []string{orgB},
		},
		{
			name: "caller-managed prefers the verified org over a trusted request field", method: addNote, unaryOnly: true,
			ctx: verified(orgA), req: &examplev1.AddNoteRequest{OrganizationId: orgB}, trustRequest: true, wantCalled: true,
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "caller-managed passes the handler error through", method: addNote,
			ctx: verified(orgA), handlerErr: errHandler, wantErr: errHandler, wantCalled: true,
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "caller-managed without any org still runs the handler", method: addNote,
			ctx: context.Background(), wantCalled: true,
		},
		{
			name: "enforced answers InvalidArgument for an org that is not a UUID", method: listNotes,
			ctx: verified("not-a-uuid"), wantCode: codes.InvalidArgument,
		},
		{
			name: "caller-managed answers InvalidArgument for an org that is not a UUID", method: addNote,
			ctx: verified("not-a-uuid"), wantCode: codes.InvalidArgument,
		},
		{
			name: "enforced passes the canonical org on", method: listNotes,
			ctx: verified(strings.ToUpper(orgA)), wantCalled: true,
			wantNewContext: []string{orgA + "/0"}, wantFinished: []error{nil},
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "a scope value the enum does not define needs an org, as enforced does", method: unknown,
			ctx: context.Background(), wantCode: codes.FailedPrecondition,
		},
		{
			name: "a scope value the enum does not define opens an enforced scope", method: unknown,
			ctx: verified(orgA), wantCalled: true,
			wantNewContext: []string{orgA + "/0"}, wantFinished: []error{nil},
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
		{
			name: "caller-managed passes the canonical org on", method: addNote,
			ctx: verified(strings.ToUpper(orgA)), wantCalled: true,
			wantOrgID: orgA, wantOutgoing: []string{orgA},
		},
	}
}

func (tt interceptorCase) check(t *testing.T, err error, rec *recorder, s seen) {
	t.Helper()
	switch {
	case tt.wantCode != codes.OK:
		if status.Code(err) != tt.wantCode {
			t.Fatalf("code = %v, want %v (err %v)", status.Code(err), tt.wantCode, err)
		}
	case err != tt.wantErr:
		t.Fatalf("err = %v, want %v", err, tt.wantErr)
	}
	if s.called != tt.wantCalled {
		t.Fatalf("handler called = %v, want %v", s.called, tt.wantCalled)
	}
	if !reflect.DeepEqual(rec.newContext, tt.wantNewContext) {
		t.Fatalf("NewContext calls = %q, want %q", rec.newContext, tt.wantNewContext)
	}
	if !reflect.DeepEqual(rec.finished, tt.wantFinished) {
		t.Fatalf("CommitOrRollback calls = %v, want %v", rec.finished, tt.wantFinished)
	}
	if s.orgID != tt.wantOrgID {
		t.Fatalf("handler OrgIDFromContext = %q, want %q", s.orgID, tt.wantOrgID)
	}
	if !reflect.DeepEqual(s.outgoing, tt.wantOutgoing) {
		t.Fatalf("outgoing org_id = %q, want %q", s.outgoing, tt.wantOutgoing)
	}
	// The handler and CommitOrRollback both get the context NewContext returned.
	var wantHolder any
	if len(tt.wantNewContext) == 1 {
		wantHolder, _, _ = strings.Cut(tt.wantNewContext[0], "/")
	}
	if s.called && s.holder != wantHolder {
		t.Fatalf("handler scope holder = %v, want %v", s.holder, wantHolder)
	}
	for i, h := range rec.finishedHolder {
		if h != wantHolder {
			t.Fatalf("CommitOrRollback call %d scope holder = %v, want %v", i, h, wantHolder)
		}
	}
}

func (tt interceptorCase) config(rec *recorder) grpctenant.TenantScopeConfig {
	cfg := rec.config()
	cfg.TrustIncomingMetadata = tt.trustMetadata
	cfg.TrustRequestOrgID = tt.trustRequest
	return cfg
}

func TestUnaryInterceptor(t *testing.T) {
	for _, tt := range interceptorCases() {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{commitErr: tt.commitErr}
			var s seen
			look := observe(&s)
			handler := func(ctx context.Context, _ any) (any, error) {
				look(ctx)
				return "resp", tt.handlerErr
			}
			_, err := grpctenant.NewTenantScopeInterceptor(tt.config(rec))(tt.ctx, tt.req, &grpc.UnaryServerInfo{FullMethod: tt.method}, handler)
			tt.check(t, err, rec, s)
		})
	}
}

// fakeStream is a grpc.ServerStream that only carries a context.
type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeStream) Context() context.Context { return f.ctx }

func TestStreamInterceptor(t *testing.T) {
	cases := append(interceptorCases(), interceptorCase{
		// A stream has no request message, so TrustRequestOrgID changes nothing.
		name: "caller-managed stream ignores TrustRequestOrgID", method: addNote,
		ctx: context.Background(), trustRequest: true, wantCalled: true,
	})
	for _, tt := range cases {
		if tt.unaryOnly {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{commitErr: tt.commitErr}
			var s seen
			look := observe(&s)
			err := grpctenant.NewTenantScopeStreamInterceptor(tt.config(rec))(nil, &fakeStream{ctx: tt.ctx},
				&grpc.StreamServerInfo{FullMethod: tt.method},
				func(_ any, ss grpc.ServerStream) error { look(ss.Context()); return tt.handlerErr })
			tt.check(t, err, rec, s)
		})
	}
}

// TestInterceptorPanic pins that a handler panic rolls back the scope, then
// reaches the caller, for unary and stream calls.
func TestInterceptorPanic(t *testing.T) {
	for _, method := range []string{listNotes, countAll} {
		for _, kind := range []string{"unary", "stream"} {
			t.Run(kind+" "+method, func(t *testing.T) {
				rec := &recorder{}
				func() {
					defer func() {
						if r := recover(); r != "handler bug" {
							t.Fatalf("recovered %v, want the handler's own panic", r)
						}
					}()
					if kind == "unary" {
						_, _ = grpctenant.NewTenantScopeInterceptor(rec.config())(verified(orgA), nil, &grpc.UnaryServerInfo{FullMethod: method},
							func(context.Context, any) (any, error) { panic("handler bug") })
						return
					}
					_ = grpctenant.NewTenantScopeStreamInterceptor(rec.config())(nil, &fakeStream{ctx: verified(orgA)}, &grpc.StreamServerInfo{FullMethod: method},
						func(any, grpc.ServerStream) error { panic("handler bug") })
				}()
				if len(rec.finished) != 1 || rec.finished[0] == nil || !strings.Contains(rec.finished[0].Error(), "handler did not return") {
					t.Fatalf("CommitOrRollback calls = %v, want one rollback naming the panic", rec.finished)
				}
				wantHolder := map[string]any{listNotes: orgA, countAll: sentinel}[method]
				if len(rec.finishedHolder) != 1 || rec.finishedHolder[0] != wantHolder {
					t.Fatalf("rollback scope holders = %v, want [%v]", rec.finishedHolder, wantHolder)
				}
			})
		}
	}
}

// TestLoadTenantScopesFromRegistry reads the options compiled into the example
// service: every annotated method is present with its scope, and an
// unannotated method is absent.
func TestLoadTenantScopesFromRegistry(t *testing.T) {
	scopes := grpctenant.LoadTenantScopesFromRegistry()
	want := map[string]pgtenantv1.TenantScope{
		listNotes: pgtenantv1.TenantScope_TENANT_SCOPE_ENFORCED,
		countAll:  pgtenantv1.TenantScope_TENANT_SCOPE_SYSTEM,
		addNote:   pgtenantv1.TenantScope_TENANT_SCOPE_CALLER_MANAGED,
	}
	for method, scope := range want {
		got, ok := scopes[method]
		if !ok || got != scope {
			t.Errorf("%s: got (%v, %v), want %v", method, got, ok, scope)
		}
	}
	for _, m := range []string{ping, "/pgtenant.example.v1.NoteService/UnscopedPing"} {
		if _, ok := scopes[m]; ok {
			t.Errorf("%s has no tenant_scope option and must be absent", m)
		}
	}
}

// TestOrgIDFromContextOrMetadata pins the lookup order: context first, then
// the first incoming org_id value.
func TestOrgIDFromContextOrMetadata(t *testing.T) {
	both := metadata.NewIncomingContext(grpctenant.WithOrgID(context.Background(), orgA), metadata.Pairs("org_id", orgB))
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"nothing", context.Background(), ""},
		{"context only", grpctenant.WithOrgID(context.Background(), orgA), orgA},
		{"metadata only", metadata.NewIncomingContext(context.Background(), metadata.Pairs("org_id", orgB)), orgB},
		{"context wins over metadata", both, orgA},
		{"first of several metadata values", metadata.NewIncomingContext(context.Background(), metadata.Pairs("org_id", orgA, "org_id", orgB)), orgA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := grpctenant.OrgIDFromContextOrMetadata(tt.ctx); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInterceptorGoexit: a handler that calls runtime.Goexit (t.Fatal) is
// rolled back, for unary and stream calls.
func TestInterceptorGoexit(t *testing.T) {
	for _, method := range []string{listNotes, countAll} {
		for _, kind := range []string{"unary", "stream"} {
			t.Run(kind+" "+method, func(t *testing.T) {
				rec := &recorder{}
				done := make(chan struct{})
				go func() {
					defer close(done)
					if kind == "unary" {
						_, _ = grpctenant.NewTenantScopeInterceptor(rec.config())(verified(orgA), nil, &grpc.UnaryServerInfo{FullMethod: method},
							func(context.Context, any) (any, error) { runtime.Goexit(); return nil, nil })
						return
					}
					_ = grpctenant.NewTenantScopeStreamInterceptor(rec.config())(nil, &fakeStream{ctx: verified(orgA)}, &grpc.StreamServerInfo{FullMethod: method},
						func(any, grpc.ServerStream) error { runtime.Goexit(); return nil })
				}()
				<-done
				if len(rec.finished) != 1 || rec.finished[0] == nil {
					t.Fatalf("CommitOrRollback calls = %v, want one rollback", rec.finished)
				}
				wantHolder := map[string]any{listNotes: orgA, countAll: sentinel}[method]
				if len(rec.finishedHolder) != 1 || rec.finishedHolder[0] != wantHolder {
					t.Fatalf("rollback scope holders = %v, want [%v]", rec.finishedHolder, wantHolder)
				}
			})
		}
	}
}

// TestInterceptorConfigChecked: both constructors panic, naming the field, on
// a config that cannot serve one of its annotated methods, so a misconfigured
// server fails at startup rather than on its first request. A field that only
// a scope the config never opens needs may be left unset.
func TestInterceptorConfigChecked(t *testing.T) {
	tests := []struct {
		name      string
		edit      func(cfg *grpctenant.TenantScopeConfig)
		wantPanic string // a substring of the panic, or "" for none
	}{
		{"a complete config", func(*grpctenant.TenantScopeConfig) {}, ""},
		{"no NewContext", func(cfg *grpctenant.TenantScopeConfig) { cfg.NewContext = nil }, "TenantScopeConfig.NewContext"},
		{"no CommitOrRollback", func(cfg *grpctenant.TenantScopeConfig) { cfg.CommitOrRollback = nil }, "TenantScopeConfig.CommitOrRollback"},
		{"no Scopes", func(cfg *grpctenant.TenantScopeConfig) { cfg.Scopes = nil }, "TenantScopeConfig.Scopes"},
		{"empty Scopes", func(cfg *grpctenant.TenantScopeConfig) { cfg.Scopes = grpctenant.TenantScopeMap{} }, ""},
		{"system methods only, no NewContext", func(cfg *grpctenant.TenantScopeConfig) {
			cfg.Scopes = grpctenant.TenantScopeMap{countAll: pgtenantv1.TenantScope_TENANT_SCOPE_SYSTEM}
			cfg.NewContext = nil
		}, "TenantScopeConfig.NewContext"},
		{"system methods only, no CommitOrRollback", func(cfg *grpctenant.TenantScopeConfig) {
			cfg.Scopes = grpctenant.TenantScopeMap{countAll: pgtenantv1.TenantScope_TENANT_SCOPE_SYSTEM}
			cfg.CommitOrRollback = nil
		}, "TenantScopeConfig.CommitOrRollback"},
		{"caller-managed methods only, no functions", func(cfg *grpctenant.TenantScopeConfig) {
			cfg.Scopes = grpctenant.TenantScopeMap{addNote: pgtenantv1.TenantScope_TENANT_SCOPE_CALLER_MANAGED}
			cfg.NewContext, cfg.CommitOrRollback = nil, nil
		}, ""},
	}
	constructors := map[string]func(grpctenant.TenantScopeConfig){
		"unary":  func(cfg grpctenant.TenantScopeConfig) { grpctenant.NewTenantScopeInterceptor(cfg) },
		"stream": func(cfg grpctenant.TenantScopeConfig) { grpctenant.NewTenantScopeStreamInterceptor(cfg) },
	}
	for _, tt := range tests {
		for kind, construct := range constructors {
			t.Run(kind+" "+tt.name, func(t *testing.T) {
				cfg := (&recorder{}).config()
				tt.edit(&cfg)
				var got string
				func() {
					defer func() {
						if r := recover(); r != nil {
							got = fmt.Sprint(r)
						}
					}()
					construct(cfg)
				}()
				switch {
				case tt.wantPanic == "" && got != "":
					t.Fatalf("panicked with %q, want no panic", got)
				case tt.wantPanic != "" && !strings.Contains(got, tt.wantPanic):
					t.Fatalf("panic = %q, want one naming %s", got, tt.wantPanic)
				}
			})
		}
	}
}

// records is a slog.Handler that keeps the records it receives.
type records struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *records) Enabled(context.Context, slog.Level) bool { return true }
func (h *records) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *records) WithGroup(string) slog.Handler            { return h }
func (h *records) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, r.Clone())
	return nil
}

func (h *records) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.recs...)
}

// attrs returns a record's attributes by key.
func attrs(r slog.Record) map[string]any {
	m := map[string]any{}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.Any(); return true })
	return m
}

var errRollback = errors.New("rollback: connection lost")

var (
	errConnLost = errors.New("conn lost")
	errH        = errors.New("h")
)

// TestRollbackFailureLogging: when the rollback after a handler that panicked
// or called runtime.Goexit fails, one Error record naming the error and the
// method goes to TenantScopeConfig.Logger. A rollback that succeeds logs
// nothing, a nil Logger logs nothing, and slog.Default never gets a record.
// The caller still gets the handler's own panic. A handler that returns, with
// nil or an error, is never logged: the caller gets the commit or rollback
// error instead.
func TestRollbackFailureLogging(t *testing.T) {
	// rollback returns the error CommitOrRollback hands back for err.
	type rollback func(err error) error
	var (
		fails             rollback = func(error) error { return errRollback }
		failsLikeTenantdb          = func(err error) error {
			// tenantdb.CommitOrRollback's wording, wrapping the handler error.
			return fmt.Errorf("rollback failed (%w) after handler error: %w", errRollback, err)
		}
		succeeds    rollback = func(err error) error { return err }
		returnsNil  rollback = func(error) error { return nil }
		commitFails          = func(error) error { return errCommit }
		connLost             = func(err error) error {
			return fmt.Errorf("rollback failed (%w) after handler error: %w", errConnLost, err)
		}
	)
	// ends says how the handler finishes: it panics, calls runtime.Goexit,
	// returns nil or returns errH.
	tests := []struct {
		name      string
		kind      string // "unary" or "stream"
		method    string
		ends      string
		rollback  rollback
		setLogger bool
		wantLog   bool
	}{
		{"a nil Logger logs nothing", "unary", listNotes, "panic", fails, false, false},
		{"a set Logger gets the failed rollback", "unary", listNotes, "panic", fails, true, true},
		{"a rollback error that wraps the handler error is logged", "unary", listNotes, "panic", failsLikeTenantdb, true, true},
		{"a rollback that succeeds logs nothing", "unary", listNotes, "panic", succeeds, true, false},
		{"a CommitOrRollback that returns nil logs nothing", "unary", listNotes, "panic", returnsNil, true, false},
		{"a stream handler", "stream", listNotes, "panic", fails, true, true},
		{"a handler that calls runtime.Goexit", "unary", listNotes, "goexit", fails, true, true},
		{"a system scope", "unary", countAll, "panic", fails, true, true},
		{"a nil Logger with a stream handler", "stream", listNotes, "panic", fails, false, false},
		{"a handler that returns nil and a failed commit", "unary", listNotes, "nil", commitFails, true, false},
		{"a stream handler that returns nil and a failed commit", "stream", listNotes, "nil", commitFails, true, false},
		{"a handler error and a failed rollback", "unary", listNotes, "error", connLost, true, false},
		{"a stream handler error and a failed rollback", "stream", listNotes, "error", connLost, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global := &records{}
			prev := slog.Default()
			slog.SetDefault(slog.New(global))
			t.Cleanup(func() { slog.SetDefault(prev) })

			own := &records{}
			rec := &recorder{}
			cfg := rec.config()
			var returned error
			cfg.CommitOrRollback = func(_ context.Context, err error) error {
				returned = tt.rollback(err)
				return returned
			}
			if tt.setLogger {
				cfg.Logger = slog.New(own)
			}
			finish := func() error {
				switch tt.ends {
				case "goexit":
					runtime.Goexit()
				case "nil":
					return nil
				case "error":
					return errH
				}
				panic("handler bug")
			}
			var recovered any
			var callErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { recovered = recover() }()
				if tt.kind == "unary" {
					_, callErr = grpctenant.NewTenantScopeInterceptor(cfg)(verified(orgA), nil, &grpc.UnaryServerInfo{FullMethod: tt.method},
						func(context.Context, any) (any, error) { return nil, finish() })
					return
				}
				callErr = grpctenant.NewTenantScopeStreamInterceptor(cfg)(nil, &fakeStream{ctx: verified(orgA)}, &grpc.StreamServerInfo{FullMethod: tt.method},
					func(any, grpc.ServerStream) error { return finish() })
			}()
			<-done
			if want := map[string]any{"panic": "handler bug"}[tt.ends]; recovered != want {
				t.Fatalf("recovered %v, want %v", recovered, want)
			}
			if (tt.ends == "nil" || tt.ends == "error") && callErr != returned {
				t.Fatalf("the call returned %v, want CommitOrRollback's %v", callErr, returned)
			}
			if got := global.all(); len(got) != 0 {
				t.Fatalf("slog.Default got %d records, want none", len(got))
			}
			got := own.all()
			if !tt.wantLog {
				if len(got) != 0 {
					t.Fatalf("Logger got %d records, want none", len(got))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("Logger got %d records, want 1", len(got))
			}
			r := got[0]
			a := attrs(r)
			if r.Level != slog.LevelError {
				t.Errorf("level = %v, want ERROR", r.Level)
			}
			if a["error"] != returned {
				t.Errorf("error attribute = %v, want the rollback error %v", a["error"], returned)
			}
			if a["method"] != tt.method {
				t.Errorf("method attribute = %v, want %s", a["method"], tt.method)
			}
		})
	}
}
