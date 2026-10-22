// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package tenantdb runs GORM queries in PostgreSQL transactions that first set
// app.current_tenant, so row-level security (RLS) policies filter rows by
// tenant.
//
// There are two ways to use it:
//
//   - [TenantTx] and [SystemTx] run a function in a short transaction for one
//     tenant or for every tenant.
//   - [NewContext] puts a scope in a context. [DB] opens the scope's transaction
//     on the first query and [CommitOrRollback] finishes it. [RunInScope] and
//     [RunInScopeValue] wrap the three for one block of code. The grpctenant
//     package calls them for each gRPC request.
//
// A scope's transaction uses one connection, so goroutines that query at the
// same time each need their own scope. A statement that overlaps another in the
// same scope fails with ErrScopeBusy. DB never runs on the plain connection:
// with no scope and no gormctx transaction in the context it returns a handle
// carrying ErrNoScope. For unscoped access use the *gorm.DB directly or
// SystemTx.
//
// Row-level security does not apply to a superuser or a role with BYPASSRLS,
// so an enforced scope or TenantTx under one fails with ErrRoleBypassesRLS
// instead of returning every tenant's rows.
package tenantdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tetriz-ai/pgtenant/gormctx"
)

// SystemTenantID is the value SystemTx and ScopeSystem write to
// app.current_tenant. Your policy decides what it grants.
const SystemTenantID = "__system__"

// Scope is the tenant isolation mode of a scope.
type Scope int

const (
	// ScopeEnforced sets app.current_tenant to the scope's org ID.
	ScopeEnforced Scope = 0
	// ScopeSystem sets app.current_tenant to SystemTenantID and
	// app.data_cutoff to -infinity.
	ScopeSystem Scope = 1
	// ScopeCallerManaged marks RPCs whose handlers call TenantTx or SystemTx
	// themselves. It is not a scope: NewContext with it gives a context whose
	// DB calls fail.
	ScopeCallerManaged Scope = 2
)

// Errors carried by the handles DB returns, and returned by the other entry
// points.
var (
	ErrInvalidOrgID     = errors.New("tenantdb: invalid org ID")
	ErrInvalidScope     = errors.New("tenantdb: invalid scope")
	ErrScopeFinished    = errors.New("tenantdb: scope already finished")
	ErrOtherDatabase    = errors.New("tenantdb: scope's transaction belongs to another database")
	ErrOuterTransaction = errors.New("tenantdb: context already carries a gormctx transaction")
	ErrCutoffChanged    = errors.New("tenantdb: data cutoff differs from the one the scope's transaction applied")
	ErrZeroCutoff       = errors.New("tenantdb: zero data cutoff")
	ErrNilDB            = errors.New("tenantdb: nil *gorm.DB")
	ErrNoScope          = errors.New("tenantdb: no scope in context")
	ErrRoleBypassesRLS  = errors.New("tenantdb: role bypasses row-level security")
	ErrScopeBusy        = errors.New("tenantdb: scope's transaction is busy with another statement")
)

type contextKey string

const (
	scopeKey      contextKey = "tenantdb_scope"
	dataCutoffKey contextKey = "tenantdb_data_cutoff"
)

// WithDataCutoff sets the date written to app.data_cutoff by the next enforced
// scope transaction or TenantTx opened with ctx. A date-based RLS policy can
// read it to hide older rows. The date is the cutoff's calendar date in its own
// time zone: 2026-01-02 03:00 in UTC+09:00 writes 2026-01-02. A zero time.Time
// is refused with ErrZeroCutoff.
func WithDataCutoff(ctx context.Context, cutoff time.Time) context.Context {
	return context.WithValue(ctx, dataCutoffKey, cutoff)
}

// DataCutoffFromContext returns the cutoff set by WithDataCutoff.
func DataCutoffFromContext(ctx context.Context) (time.Time, bool) {
	v, ok := ctx.Value(dataCutoffKey).(time.Time)
	return v, ok
}

// maxOrgInError bounds how much of an invalid org ID an error repeats.
const maxOrgInError = 64

// canonicalOrgID returns orgID as a canonical UUID, the form a policy's
// organization_id::text comparison matches. The sentinel is refused here;
// system scopes write it themselves.
func canonicalOrgID(orgID string) (string, error) {
	parsed, err := uuid.Parse(orgID)
	if err != nil {
		shown := orgID
		if len(shown) > maxOrgInError {
			shown = shown[:maxOrgInError] + "..."
		}
		return "", fmt.Errorf("%w %q: %w", ErrInvalidOrgID, shown, err)
	}
	return parsed.String(), nil
}

// settings are what a transaction writes before its first query.
type settings struct {
	tenant string // app.current_tenant
	cutoff string // app.data_cutoff, "" for none
}

// settingsFor validates a scope's org, scope and cutoff before any round trip.
func settingsFor(ctx context.Context, orgID string, scope Scope) (settings, error) {
	switch scope {
	case ScopeSystem:
		return settings{tenant: SystemTenantID, cutoff: "-infinity"}, nil
	case ScopeEnforced:
		tenant, err := canonicalOrgID(orgID)
		if err != nil {
			return settings{}, err
		}
		cutoff, err := cutoffFrom(ctx)
		if err != nil {
			return settings{}, err
		}
		return settings{tenant: tenant, cutoff: cutoff}, nil
	default:
		return settings{}, fmt.Errorf("%w: %d", ErrInvalidScope, scope)
	}
}

// cutoffFrom returns the date WithDataCutoff put in ctx, or "" for none.
func cutoffFrom(ctx context.Context) (string, error) {
	cutoff, ok := DataCutoffFromContext(ctx)
	if !ok {
		return "", nil
	}
	if cutoff.IsZero() {
		return "", ErrZeroCutoff
	}
	return cutoff.Format("2006-01-02"), nil
}

// apply writes s inside tx. The round trip that sets app.current_tenant also
// reads whether the role in effect bypasses row-level security. That is checked
// on every transaction, not cached per pool, because SET ROLE and pooled
// sessions can change the role a connection runs as. A tenant setting under
// such a role is refused: the policy would not filter its rows. A system
// setting sees every row anyway, so it is not.
func (s settings) apply(tx *gorm.DB) error {
	role, bypassRLS, err := setTenant(tx, s.tenant)
	if err != nil {
		return fmt.Errorf("tenantdb: set app.current_tenant: %w", err)
	}
	if bypassRLS && s.tenant != SystemTenantID {
		return fmt.Errorf("%w: role %q is a superuser or has BYPASSRLS", ErrRoleBypassesRLS, role)
	}
	if s.cutoff != "" {
		if err := tx.Exec("SELECT set_config('app.data_cutoff', ?, true)", s.cutoff).Error; err != nil {
			return fmt.Errorf("tenantdb: set app.data_cutoff: %w", err)
		}
	}
	return nil
}

// setTenant writes tenant to app.current_tenant for tx's transaction and
// returns the role in effect and whether row-level security skips it. The
// columns are read by position, so the caller's gorm naming strategy cannot
// hide them.
func setTenant(tx *gorm.DB, tenant string) (role string, bypassRLS bool, err error) {
	rows, err := tx.Raw(`SELECT set_config('app.current_tenant', ?, true), current_user,
		(SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user)`, tenant).Rows()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", false, err
		}
		return "", false, errors.New("no row returned")
	}
	var set string
	if err := rows.Scan(&set, &role, &bypassRLS); err != nil {
		return "", false, err
	}
	return role, bypassRLS, rows.Close()
}

// scope is what NewContext places in a context.
type scope struct {
	mu       sync.Mutex
	orgID    string
	scope    Scope
	tx       *gorm.DB      // the open transaction; nil until the first DB call
	pool     gorm.ConnPool // the pool of the *gorm.DB the transaction opened on
	cutoff   string        // app.data_cutoff the transaction applied
	finished bool
}

// NewContext returns a context carrying a scope for orgID. It opens no
// transaction; the first DB call does. Use one scope per unit of work, and
// finish it with CommitOrRollback.
func NewContext(ctx context.Context, orgID string, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey, &scope{orgID: orgID, scope: s})
}

func scopeFrom(ctx context.Context) *scope {
	s, _ := ctx.Value(scopeKey).(*scope)
	return s
}

// HasOpenTx reports whether ctx carries a scope whose transaction is open.
// Check it before a slow outbound call: a transaction held open across one
// idles until PostgreSQL's idle_in_transaction_session_timeout kills it.
func HasOpenTx(ctx context.Context) bool {
	s := scopeFrom(ctx)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx != nil && !s.finished
}

// rejectedPool prevents GORM operations such as Begin and Transaction from
// acquiring a connection even when they do not short-circuit on DB.Error.
type rejectedPool struct{ err error }

func (p rejectedPool) PrepareContext(context.Context, string) (*sql.Stmt, error) { return nil, p.err }
func (p rejectedPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, p.err
}
func (p rejectedPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, p.err
}
func (p rejectedPool) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return rejectedRows.QueryRowContext(context.Background(), "")
}
func (p rejectedPool) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	return nil, p.err
}

// database/sql is needed to construct a Row whose Scan reports an error.
// Its connector refuses acquisition, so this pool never owns a connection.
var rejectedRows = sql.OpenDB(rejectedConnector{})

type rejectedConnector struct{}

func (rejectedConnector) Connect(context.Context) (driver.Conn, error) { return nil, ErrNoScope }
func (rejectedConnector) Driver() driver.Driver                        { return rejectedDriver{} }

type rejectedDriver struct{}

func (rejectedDriver) Open(string) (driver.Conn, error) { return nil, ErrNoScope }

var errorBase = func() *gorm.DB {
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: rejectedPool{ErrNoScope}}), &gorm.Config{DisableAutomaticPing: true, Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		panic("tenantdb: open the error handle: " + err.Error())
	}
	return db
}()

// failed isolates the error handle from both the caller's pool and transaction.
func failed(ctx context.Context, err error) *gorm.DB {
	tx := errorBase.Session(&gorm.Session{NewDB: true, Context: ctx})
	tx.Statement.ConnPool = rejectedPool{err}
	_ = tx.AddError(err)
	return tx
}

// DB returns the handle a DAO method queries through.
//
// With no scope in ctx it returns the gormctx transaction in ctx, if any, and
// otherwise a handle carrying ErrNoScope: it never runs on the plain
// connection. For unscoped access use the *gorm.DB directly or SystemTx. With a
// scope, the first call opens the transaction on defaultDB and writes
// app.current_tenant (and app.data_cutoff when set); every later call returns
// the same transaction, bound to that call's ctx and honouring the gormctx lock
// flag.
//
// DB never panics on a scope. It returns a handle whose Error is set when the
// org ID, scope or cutoff is invalid, when BEGIN or a SET fails, when an
// enforced scope's role bypasses row-level security, when the scope is
// finished, when defaultDB is nil or another database than the one the
// transaction opened on, when ctx carries a gormctx transaction, or when a
// later call's cutoff differs from the one the transaction applied.
//
// The scope's transaction runs one statement at a time. A statement through
// the handle fails with ErrScopeBusy when another statement in the scope is
// running or has rows open, as when two goroutines share the scope. The one
// gap is a *sql.Row from Row, whose rows stay out of sight until its Scan: scan
// it before the scope's next statement.
func DB(ctx context.Context, defaultDB *gorm.DB) *gorm.DB {
	s := scopeFrom(ctx)
	if s == nil {
		if carried, ok := gormctx.FromContext(ctx); ok && carried.Statement != nil {
			if _, transactional := carried.Statement.ConnPool.(gorm.TxCommitter); transactional {
				return gormctx.FromContextOrDefault(ctx, defaultDB)
			}
		}
		return failed(ctx, ErrNoScope)
	}
	if defaultDB == nil {
		return failed(ctx, ErrNilDB)
	}
	if _, ok := gormctx.FromContext(ctx); ok {
		return failed(ctx, ErrOuterTransaction)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return failed(ctx, ErrScopeFinished)
	}
	if s.tx != nil {
		if defaultDB.Statement.ConnPool != s.pool {
			return failed(ctx, ErrOtherDatabase)
		}
		if s.scope == ScopeEnforced {
			cutoff, err := cutoffFrom(ctx)
			if err != nil {
				return failed(ctx, err)
			}
			if cutoff != "" && cutoff != s.cutoff {
				return failed(ctx, ErrCutoffChanged)
			}
		}
		return gormctx.FromContextOrDefault(ctx, s.tx)
	}

	set, err := settingsFor(ctx, s.orgID, s.scope)
	if err != nil {
		return failed(ctx, err)
	}
	// The transaction outlives the first call's ctx: each later call binds it
	// to its own ctx, so the first ctx's cancellation must not roll it back.
	tx := defaultDB.WithContext(context.WithoutCancel(ctx)).Begin()
	if tx.Error != nil {
		return failed(ctx, fmt.Errorf("tenantdb: begin: %w", tx.Error))
	}
	if err := set.apply(tx); err != nil {
		tx.Rollback()
		return failed(ctx, err)
	}
	oneStatementAtATime(tx)
	s.tx, s.pool, s.cutoff = tx, defaultDB.Statement.ConnPool, set.cutoff
	return gormctx.FromContextOrDefault(ctx, tx)
}

// CommitOrRollback finishes the scope in ctx: it rolls back when err is not
// nil and commits otherwise. It returns err unchanged when there is no scope,
// when the scope opened no transaction, and on every call after the first.
// A rollback failure is joined with err; a commit failure is returned.
func CommitOrRollback(ctx context.Context, err error) error {
	s := scopeFrom(ctx)
	if s == nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return err
	}
	s.finished = true
	if s.tx == nil {
		return err
	}
	if err != nil {
		if rbErr := s.tx.Rollback().Error; rbErr != nil {
			return fmt.Errorf("rollback failed (%w) after handler error: %w", rbErr, err)
		}
		return err
	}
	if cErr := s.tx.Commit().Error; cErr != nil {
		return fmt.Errorf("commit failed: %w", cErr)
	}
	return nil
}

// errFnDidNotReturn is the error RunInScope rolls back with when fn panics or
// calls runtime.Goexit.
var errFnDidNotReturn = errors.New("tenantdb: fn did not return")

// RunInScope runs fn with a new scope for orgID, then commits when fn returns
// nil and rolls back otherwise. fn's error is returned unwrapped. When fn
// panics or calls runtime.Goexit it rolls back, and a panic keeps propagating.
// The new scope shadows any scope in ctx. scope must be ScopeEnforced or
// ScopeSystem.
func RunInScope(ctx context.Context, orgID string, scope Scope, fn func(ctx context.Context) error) error {
	if scope != ScopeEnforced && scope != ScopeSystem {
		return fmt.Errorf("%w: %d", ErrInvalidScope, scope)
	}
	scopedCtx := NewContext(ctx, orgID, scope)
	// Roll back when fn does not return: a panic (which keeps propagating) or
	// runtime.Goexit from t.Fatal in fn.
	returned := false
	defer func() {
		if !returned {
			_ = CommitOrRollback(scopedCtx, errFnDidNotReturn)
		}
	}()
	err := fn(scopedCtx)
	returned = true
	return CommitOrRollback(scopedCtx, err)
}

// RunInScopeValue is RunInScope for a fn that returns a value. On any error it
// returns the zero value of T.
func RunInScopeValue[T any](ctx context.Context, orgID string, scope Scope, fn func(ctx context.Context) (T, error)) (T, error) {
	var result T
	err := RunInScope(ctx, orgID, scope, func(ctx context.Context) error {
		var err error
		result, err = fn(ctx)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}
