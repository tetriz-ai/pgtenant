// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb_test

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/gormctx"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// TestTenantIsolation: under FORCE ROW LEVEL SECURITY each entry point shows a
// tenant its own rows only, and a write for another tenant fails WITH CHECK.
func TestTenantIsolation(t *testing.T) {
	db := newGormDB(t, forceRLS)
	seed(t, db, orgA, orgB)

	read := func(t *testing.T, run func(fn func(*gorm.DB) error) error) []string {
		t.Helper()
		var got []string
		if err := run(func(tx *gorm.DB) error { got = bodies(t, tx); return nil }); err != nil {
			t.Fatal(err)
		}
		return got
	}
	tests := []struct {
		name string
		read func(t *testing.T) []string
		want []string
	}{
		{"TenantTx", func(t *testing.T) []string {
			return read(t, func(fn func(*gorm.DB) error) error { return tenantdb.TenantTx(context.Background(), db, orgA, fn) })
		}, []string{"note of " + orgA}},
		{"a scope through DB", func(t *testing.T) []string {
			got, err := tenantdb.RunInScopeValue(context.Background(), orgB, tenantdb.ScopeEnforced,
				func(ctx context.Context) ([]string, error) { return bodies(t, tenantdb.DB(ctx, db)), nil })
			if err != nil {
				t.Fatal(err)
			}
			return got
		}, []string{"note of " + orgB}},
		{"SystemTx sees every tenant", func(t *testing.T) []string {
			return read(t, func(fn func(*gorm.DB) error) error { return tenantdb.SystemTx(context.Background(), db, fn) })
		}, []string{"note of " + orgA, "note of " + orgB}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.read(t); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("visible notes = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("a write for another tenant fails WITH CHECK", func(t *testing.T) {
		err := tenantdb.TenantTx(context.Background(), db, orgA, func(tx *gorm.DB) error {
			return tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, 'sneaky')", orgB).Error
		})
		if err == nil || !strings.Contains(err.Error(), "row-level security") {
			t.Fatalf("err = %v, want a row-level security violation", err)
		}
	})
}

// TestOwnerBypassesRLSWithoutForce: the table owner is not subject to its
// policies unless the table is FORCEd (a PostgreSQL rule).
func TestOwnerBypassesRLSWithoutForce(t *testing.T) {
	db := newGormDB(t)
	seed(t, db, orgA, orgB)
	var got []string
	if err := tenantdb.TenantTx(context.Background(), db, orgA, func(tx *gorm.DB) error { got = bodies(t, tx); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("owner saw %q, want both tenants' notes", got)
	}
}

// TestTransactionOutcomes: commit on nil, rollback on an error (returned
// unwrapped) and on a panic (re-raised), for each entry point.
func TestTransactionOutcomes(t *testing.T) {
	errBoom := errors.New("boom")
	insert := func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, 'new')", orgA).Error
	}
	entries := map[string]func(db *gorm.DB, fn func(*gorm.DB) error) error{
		"RunInScope": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.RunInScope(context.Background(), orgA, tenantdb.ScopeEnforced, func(ctx context.Context) error { return fn(tenantdb.DB(ctx, db)) })
		},
		"TenantTx": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.TenantTx(context.Background(), db, orgA, fn)
		},
		"SystemTx": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.SystemTx(context.Background(), db, fn)
		},
	}
	outcomes := []struct {
		name      string
		fn        func(tx *gorm.DB) error
		wantErr   error
		wantPanic bool
		wantRows  int
	}{
		{"nil commits", insert, nil, false, 2},
		{"an error rolls back", func(tx *gorm.DB) error { _ = insert(tx); return errBoom }, errBoom, false, 1},
		{"a panic rolls back", func(tx *gorm.DB) error { _ = insert(tx); panic("bug") }, nil, true, 1},
	}
	for entry, run := range entries {
		for _, tt := range outcomes {
			t.Run(entry+"/"+tt.name, func(t *testing.T) {
				db := newGormDB(t, forceRLS)
				seed(t, db, orgA)
				var err error
				panicked := func() (p bool) {
					defer func() { p = recover() != nil }()
					err = run(db, tt.fn)
					return false
				}()
				if panicked != tt.wantPanic || err != tt.wantErr {
					t.Fatalf("panicked=%v err=%v, want %v %v", panicked, err, tt.wantPanic, tt.wantErr)
				}
				if got := len(systemBodies(t, db)); got != tt.wantRows {
					t.Fatalf("rows = %d, want %d", got, tt.wantRows)
				}
			})
		}
	}
}

// TestRunInScopeValueZeroOnError: the value is the zero value when fn fails.
func TestRunInScopeValueZeroOnError(t *testing.T) {
	db := newGormDB(t)
	got, err := tenantdb.RunInScopeValue(context.Background(), orgA, tenantdb.ScopeEnforced,
		func(ctx context.Context) (int, error) { tenantdb.DB(ctx, db); return 42, errors.New("fails") })
	if err == nil || got != 0 {
		t.Fatalf("got (%d, %v), want (0, error)", got, err)
	}
}

// TestNestedRunInScope: an inner scope shadows the outer one, which resumes.
func TestNestedRunInScope(t *testing.T) {
	db := newGormDB(t)
	var outer, inner, after string
	err := tenantdb.RunInScope(context.Background(), orgA, tenantdb.ScopeEnforced, func(ctx context.Context) error {
		outer = setting(t, tenantdb.DB(ctx, db), "app.current_tenant")
		if err := tenantdb.RunInScope(ctx, orgB, tenantdb.ScopeEnforced, func(ctx context.Context) error {
			inner = setting(t, tenantdb.DB(ctx, db), "app.current_tenant")
			return nil
		}); err != nil {
			return err
		}
		after = setting(t, tenantdb.DB(ctx, db), "app.current_tenant")
		return nil
	})
	if err != nil || outer != orgA || inner != orgB || after != orgA {
		t.Fatalf("outer %q inner %q after %q err %v", outer, inner, after, err)
	}
}

// TestScopeValues: only ScopeEnforced and ScopeSystem open a transaction.
func TestScopeValues(t *testing.T) {
	db := newGormDB(t)
	tests := []struct {
		name    string
		scope   tenantdb.Scope
		want    string
		wantErr error
	}{
		{"ScopeEnforced writes the org", tenantdb.ScopeEnforced, orgA, nil},
		{"ScopeSystem writes the sentinel", tenantdb.ScopeSystem, tenantdb.SystemTenantID, nil},
		{"ScopeCallerManaged is refused", tenantdb.ScopeCallerManaged, "", tenantdb.ErrInvalidScope},
		{"an unknown value is refused", tenantdb.Scope(7), "", tenantdb.ErrInvalidScope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tenantdb.NewContext(context.Background(), orgA, tt.scope)
			defer func() { _ = tenantdb.CommitOrRollback(ctx, errors.New("teardown")) }()
			h := tenantdb.DB(ctx, db)
			if !errors.Is(h.Error, tt.wantErr) {
				t.Fatalf("Error = %v, want %v", h.Error, tt.wantErr)
			}
			if tt.wantErr == nil {
				if got := setting(t, h, "app.current_tenant"); got != tt.want {
					t.Fatalf("app.current_tenant = %q, want %q", got, tt.want)
				}
			}
		})
	}
	t.Run("RunInScope refuses ScopeCallerManaged before running fn", func(t *testing.T) {
		ran := false
		err := tenantdb.RunInScope(context.Background(), orgA, tenantdb.ScopeCallerManaged, func(context.Context) error { ran = true; return nil })
		if !errors.Is(err, tenantdb.ErrInvalidScope) || ran {
			t.Fatalf("err = %v, ran = %v", err, ran)
		}
	})
}

// TestDBGuards: the ways DB refuses to run a query, each with its error.
func TestDBGuards(t *testing.T) {
	db := newGormDB(t)
	other := newGormDB(t)
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		call func(t *testing.T) *gorm.DB
		want error
	}{
		{"a second database under one scope", func(t *testing.T) *gorm.DB {
			ctx := scoped(t, orgA)
			tenantdb.DB(ctx, db).Exec("SELECT 1")
			return tenantdb.DB(ctx, other)
		}, tenantdb.ErrOtherDatabase},
		{"a gormctx transaction already in ctx", func(t *testing.T) *gorm.DB {
			outer := db.Begin()
			t.Cleanup(func() { outer.Rollback() })
			return tenantdb.DB(gormctx.New(scoped(t, orgA), outer), db)
		}, tenantdb.ErrOuterTransaction},
		{"a finished scope", func(t *testing.T) *gorm.DB {
			ctx := scoped(t, orgA)
			tenantdb.DB(ctx, db).Exec("SELECT 1")
			_ = tenantdb.CommitOrRollback(ctx, nil)
			return tenantdb.DB(context.WithValue(ctx, childKey{}, "child"), db)
		}, tenantdb.ErrScopeFinished},
		{"a nil database", func(t *testing.T) *gorm.DB { return tenantdb.DB(scoped(t, orgA), nil) }, tenantdb.ErrNilDB},
		{"a zero cutoff", func(t *testing.T) *gorm.DB {
			return tenantdb.DB(tenantdb.WithDataCutoff(scoped(t, orgA), time.Time{}), db)
		}, tenantdb.ErrZeroCutoff},
		{"a cutoff added after the transaction opened", func(t *testing.T) *gorm.DB {
			ctx := scoped(t, orgA)
			tenantdb.DB(ctx, db).Exec("SELECT 1")
			return tenantdb.DB(tenantdb.WithDataCutoff(ctx, cutoff), db)
		}, tenantdb.ErrCutoffChanged},
		{"a different cutoff than the one applied", func(t *testing.T) *gorm.DB {
			ctx := tenantdb.WithDataCutoff(scoped(t, orgA), cutoff)
			tenantdb.DB(ctx, db).Exec("SELECT 1")
			return tenantdb.DB(tenantdb.WithDataCutoff(ctx, cutoff.AddDate(0, 0, 1)), db)
		}, tenantdb.ErrCutoffChanged},
		{"an invalid org ID", func(t *testing.T) *gorm.DB { return tenantdb.DB(scoped(t, "not-a-uuid"), db) }, tenantdb.ErrInvalidOrgID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.call(t)
			if !errors.Is(h.Error, tt.want) {
				t.Fatalf("Error = %v, want %v", h.Error, tt.want)
			}
			var n int
			if err := h.Raw("SELECT 1").Scan(&n).Error; !errors.Is(err, tt.want) || n != 0 {
				t.Fatalf("a query through the handle ran (n=%d, err=%v)", n, err)
			}
		})
	}
	t.Run("the same cutoff on a later call is accepted", func(t *testing.T) {
		ctx := tenantdb.WithDataCutoff(scoped(t, orgA), cutoff)
		tenantdb.DB(ctx, db).Exec("SELECT 1")
		if err := tenantdb.DB(tenantdb.WithDataCutoff(ctx, cutoff.Add(time.Hour)), db).Exec("SELECT 1").Error; err != nil {
			t.Fatal(err)
		}
	})
}

// TestDBBindsEachCallsContext: later calls use their own context, and the
// first call's context ending does not end the transaction.
func TestDBBindsEachCallsContext(t *testing.T) {
	db := newGormDB(t)
	t.Run("a later deadline applies", func(t *testing.T) {
		ctx := scoped(t, orgA)
		tenantdb.DB(ctx, db).Exec("SELECT 1")
		short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := tenantdb.DB(short, db).Exec("SELECT pg_sleep(1)").Error
		if err == nil || time.Since(start) > 500*time.Millisecond {
			t.Fatalf("err = %v after %v, want the 50ms deadline to end the query", err, time.Since(start))
		}
	})
	t.Run("cancelling the first call's context keeps the scope usable", func(t *testing.T) {
		ctx := scoped(t, orgA)
		first, cancel := context.WithCancel(ctx)
		tenantdb.DB(first, db).Exec("SELECT 1")
		cancel()
		time.Sleep(50 * time.Millisecond)
		if err := tenantdb.DB(ctx, db).Exec("SELECT 1").Error; err != nil {
			t.Fatalf("query after the first ctx ended: %v", err)
		}
		if err := tenantdb.CommitOrRollback(ctx, nil); err != nil {
			t.Fatalf("commit: %v", err)
		}
	})
}

// TestDBHonoursGormctxLock: the lock flag adds FOR UPDATE inside a scope.
func TestDBHonoursGormctxLock(t *testing.T) {
	db := newGormDB(t)
	ctx := gormctx.NewWithLockKey(scoped(t, orgA))
	var ids []string
	stmt := tenantdb.DB(ctx, db).Session(&gorm.Session{DryRun: true}).Table("notes").Select("id").Find(&ids).Statement
	if !strings.HasSuffix(stmt.SQL.String(), "FOR UPDATE") {
		t.Fatalf("SQL %q lacks FOR UPDATE", stmt.SQL.String())
	}
}

// TestFailedSetReportsItsError: when a SET fails, the handle carries it.
func TestFailedSetReportsItsError(t *testing.T) {
	tests := []struct {
		name, match string
		ctx         func(t *testing.T) context.Context
	}{
		{"app.current_tenant", "set_config('app.current_tenant'", func(t *testing.T) context.Context { return scoped(t, orgA) }},
		{"app.data_cutoff", "set_config('app.data_cutoff'", func(t *testing.T) context.Context {
			return tenantdb.WithDataCutoff(scoped(t, orgA), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newGormDB(t)
			injected := errors.New("injected failure")
			failStatements(t, db, tt.match, injected)
			if h := tenantdb.DB(tt.ctx(t), db); !errors.Is(h.Error, injected) {
				t.Fatalf("Error = %v, want the injected failure", h.Error)
			}
		})
	}
}

// failStatements makes every statement on db whose SQL contains match fail
// with err before it is sent.
func failStatements(t *testing.T, db *gorm.DB, match string, err error) {
	t.Helper()
	fail := func(d *gorm.DB) {
		if strings.Contains(d.Statement.SQL.String(), match) {
			_ = d.AddError(err)
		}
	}
	if err := db.Callback().Raw().Before("gorm:raw").Register("fail_set", fail); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().Before("gorm:row").Register("fail_set", fail); err != nil {
		t.Fatal(err)
	}
}

// TestCommitOrRollback: a finished scope ignores a second call, and a failed
// rollback is joined with the handler's error.
func TestCommitOrRollback(t *testing.T) {
	db := newGormDB(t)
	t.Run("a second call is a no-op", func(t *testing.T) {
		ctx := scoped(t, orgA)
		tenantdb.DB(ctx, db).Exec("SELECT 1")
		if err := tenantdb.CommitOrRollback(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := tenantdb.CommitOrRollback(ctx, nil); err != nil {
			t.Fatalf("second call = %v, want nil", err)
		}
	})
	t.Run("a failed rollback is joined with the handler error", func(t *testing.T) {
		ctx := scoped(t, orgA)
		var pid int
		tenantdb.DB(ctx, db).Raw("SELECT pg_backend_pid()").Scan(&pid)
		if err := db.Exec("SELECT pg_terminate_backend(?)", pid).Error; err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		errHandler := errors.New("handler failed")
		err := tenantdb.CommitOrRollback(ctx, errHandler)
		if !errors.Is(err, errHandler) || !strings.Contains(err.Error(), "rollback failed") {
			t.Fatalf("err = %v, want the rollback failure joined with the handler error", err)
		}
	})
}

// TestConcurrentFirstCallOpensOneTransaction: goroutines opening one fresh
// scope at once share one transaction.
func TestConcurrentFirstCallOpensOneTransaction(t *testing.T) {
	db := newGormDB(t)
	ctx := scoped(t, orgA)
	// No goroutine queries: only the opening is shared. A second opened
	// transaction would be left idle in a transaction after the commit.
	const n = 16
	done := make(chan struct{}, n)
	start := make(chan struct{})
	for range n {
		go func() {
			<-start
			tenantdb.DB(ctx, db)
			done <- struct{}{}
		}()
	}
	close(start)
	for range n {
		<-done
	}
	var name string
	db.Raw("SELECT current_database()").Scan(&name)
	if err := tenantdb.CommitOrRollback(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var idle int64
	db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = ? AND state LIKE 'idle in transaction%'", name).Scan(&idle)
	if idle != 0 {
		t.Fatalf("%d backends left idle in a transaction", idle)
	}
}

// TestDataCutoff: the cutoff's own-zone date is applied by a scope and by
// TenantTx; system scopes write -infinity.
func TestDataCutoff(t *testing.T) {
	db := newGormDB(t)
	east := time.FixedZone("UTC+09:00", 9*3600)
	west := time.FixedZone("UTC-08:00", -8*3600)
	dates := []struct {
		name   string
		cutoff time.Time
		want   string
	}{
		{"east of UTC, early morning", time.Date(2026, 1, 2, 3, 0, 0, 0, east), "2026-01-02"},
		{"east of UTC, local midnight", time.Date(2026, 1, 2, 0, 0, 0, 0, east), "2026-01-02"},
		{"UTC", time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC), "2026-01-02"},
		{"west of UTC, just after midnight", time.Date(2026, 1, 2, 1, 0, 0, 0, west), "2026-01-02"},
		{"west of UTC, late evening", time.Date(2026, 1, 1, 23, 0, 0, 0, west), "2026-01-01"},
	}
	for _, tt := range dates {
		t.Run("scope/"+tt.name, func(t *testing.T) {
			if got := setting(t, tenantdb.DB(tenantdb.WithDataCutoff(scoped(t, orgA), tt.cutoff), db), "app.data_cutoff"); got != tt.want {
				t.Fatalf("app.data_cutoff = %q, want %q", got, tt.want)
			}
		})
	}
	cutoff := tenantdb.WithDataCutoff(context.Background(), time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC))
	t.Run("TenantTx applies the cutoff", func(t *testing.T) {
		var got string
		if err := tenantdb.TenantTx(cutoff, db, orgA, func(tx *gorm.DB) error { got = setting(t, tx, "app.data_cutoff"); return nil }); err != nil || got != "2026-01-02" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("SystemTx writes -infinity", func(t *testing.T) {
		var got string
		if err := tenantdb.SystemTx(cutoff, db, func(tx *gorm.DB) error { got = setting(t, tx, "app.data_cutoff"); return nil }); err != nil || got != "-infinity" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("TenantTx refuses a zero cutoff", func(t *testing.T) {
		err := tenantdb.TenantTx(tenantdb.WithDataCutoff(context.Background(), time.Time{}), db, orgA, func(*gorm.DB) error { return nil })
		if !errors.Is(err, tenantdb.ErrZeroCutoff) {
			t.Fatalf("err = %v", err)
		}
	})
}

type childKey struct{}

// scoped returns a fresh enforced scope for org that the test finishes.
func scoped(t *testing.T, org string) context.Context {
	t.Helper()
	ctx := tenantdb.NewContext(context.Background(), org, tenantdb.ScopeEnforced)
	t.Cleanup(func() { _ = tenantdb.CommitOrRollback(ctx, errors.New("teardown")) })
	return ctx
}

// setting reads a setting through h.
func setting(t *testing.T, h *gorm.DB, name string) string {
	t.Helper()
	var got string
	if err := h.Raw("SELECT coalesce(current_setting(?, true), '')", name).Scan(&got).Error; err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return got
}

// systemBodies reads every note in system scope.
func systemBodies(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var got []string
	if err := tenantdb.SystemTx(context.Background(), db, func(tx *gorm.DB) error { got = bodies(t, tx); return nil }); err != nil {
		t.Fatalf("SystemTx: %v", err)
	}
	return got
}

// TestGoexitRollsBack: runtime.Goexit in fn (what t.Fatal does) rolls the
// transaction back instead of leaving it idle in a transaction.
func TestGoexitRollsBack(t *testing.T) {
	entries := map[string]func(db *gorm.DB, fn func(*gorm.DB) error) error{
		"TenantTx": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.TenantTx(context.Background(), db, orgA, fn)
		},
		"SystemTx": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.SystemTx(context.Background(), db, fn)
		},
		"RunInScope": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.RunInScope(context.Background(), orgA, tenantdb.ScopeEnforced, func(ctx context.Context) error { return fn(tenantdb.DB(ctx, db)) })
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			db := newGormDB(t, forceRLS)
			seed(t, db, orgA)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = run(db, func(tx *gorm.DB) error {
					tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, 'dropped')", orgA)
					runtime.Goexit()
					return nil
				})
			}()
			<-done
			if n := idleInTransaction(t, db); n != 0 {
				t.Fatalf("%d backends left idle in a transaction", n)
			}
			if got := len(systemBodies(t, db)); got != 1 {
				t.Fatalf("rows = %d, want 1 (the insert rolled back)", got)
			}
		})
	}
}

// TestFailedCommitIsReported: a constraint checked at COMMIT fails the call,
// and nothing persists.
func TestFailedCommitIsReported(t *testing.T) {
	entries := map[string]func(db *gorm.DB, fn func(*gorm.DB) error) error{
		"TenantTx": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.TenantTx(context.Background(), db, orgA, fn)
		},
		"SystemTx": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.SystemTx(context.Background(), db, fn)
		},
		"RunInScope": func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.RunInScope(context.Background(), orgA, tenantdb.ScopeEnforced, func(ctx context.Context) error { return fn(tenantdb.DB(ctx, db)) })
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			db := newGormDB(t)
			if err := db.Exec("CREATE TABLE deferred (id int, CONSTRAINT deferred_pk PRIMARY KEY (id) DEFERRABLE INITIALLY DEFERRED)").Error; err != nil {
				t.Fatal(err)
			}
			err := run(db, func(tx *gorm.DB) error {
				return tx.Exec("INSERT INTO deferred VALUES (1), (1)").Error
			})
			if err == nil || !strings.Contains(err.Error(), "commit") {
				t.Fatalf("err = %v, want a commit failure", err)
			}
			var n int64
			db.Raw("SELECT count(*) FROM deferred").Scan(&n)
			if n != 0 {
				t.Fatalf("%d rows persisted", n)
			}
		})
	}
}

// TestFailedSetInTenantTx: when TenantTx's or SystemTx's SET fails, the call
// returns that error and fn never runs.
func TestFailedSetInTenantTx(t *testing.T) {
	cutoff := tenantdb.WithDataCutoff(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	tests := []struct {
		name, match string
		run         func(db *gorm.DB, fn func(*gorm.DB) error) error
	}{
		{"TenantTx tenant", "set_config('app.current_tenant'", func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.TenantTx(context.Background(), db, orgA, fn)
		}},
		{"TenantTx cutoff", "set_config('app.data_cutoff'", func(db *gorm.DB, fn func(*gorm.DB) error) error { return tenantdb.TenantTx(cutoff, db, orgA, fn) }},
		{"SystemTx tenant", "set_config('app.current_tenant'", func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.SystemTx(context.Background(), db, fn)
		}},
		{"SystemTx cutoff", "set_config('app.data_cutoff'", func(db *gorm.DB, fn func(*gorm.DB) error) error {
			return tenantdb.SystemTx(context.Background(), db, fn)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newGormDB(t)
			injected := errors.New("injected failure")
			failStatements(t, db, tt.match, injected)
			ran := false
			err := tt.run(db, func(*gorm.DB) error { ran = true; return nil })
			if !errors.Is(err, injected) || ran {
				t.Fatalf("err = %v, fn ran = %v; want the injected error and no fn", err, ran)
			}
		})
	}
}

// idleInTransaction counts backends on db's database left idle in a
// transaction.
func idleInTransaction(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var name string
	db.Raw("SELECT current_database()").Scan(&name)
	var n int64
	db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = ? AND state LIKE 'idle in transaction%'", name).Scan(&n)
	return n
}

// TestDAOOutsideAScope: a DAO that calls DB with a context carrying no scope,
// here inside TenantTx's fn, gets ErrNoScope instead of an empty result.
func TestDAOOutsideAScope(t *testing.T) {
	db := newGormDB(t, forceRLS)
	seed(t, db, orgA)
	ctx := context.Background()
	list := func(ctx context.Context) ([]string, error) {
		var out []string
		err := tenantdb.DB(ctx, db).Raw("SELECT body FROM notes").Scan(&out).Error
		return out, err
	}
	var got []string
	err := tenantdb.TenantTx(ctx, db, orgA, func(*gorm.DB) error {
		var err error
		got, err = list(ctx)
		return err
	})
	if !errors.Is(err, tenantdb.ErrNoScope) || got != nil {
		t.Fatalf("got (%q, %v), want ErrNoScope", got, err)
	}
}

// TestNoTenantSetReadsNoRows: a query that sets no tenant, through the plain
// *gorm.DB or through a gormctx transaction with no scope, gets no rows and no
// error under FORCE, while a gormctx transaction from SystemTx joins its scope.
func TestNoTenantSetReadsNoRows(t *testing.T) {
	db := newGormDB(t, forceRLS)
	seed(t, db, orgA, orgB)
	ctx := context.Background()
	tests := []struct {
		name string
		read func(t *testing.T) []string
		want []string
	}{
		{"the plain *gorm.DB", func(t *testing.T) []string { return bodies(t, db) }, nil},
		{"tenantdb.DB on a gormctx transaction with no scope", func(t *testing.T) []string {
			var got []string
			if err := db.Transaction(func(tx *gorm.DB) error {
				got = bodies(t, tenantdb.DB(gormctx.New(ctx, tx), db))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return got
		}, nil},
		{"tenantdb.DB on the gormctx transaction of SystemTx", func(t *testing.T) []string {
			var got []string
			if err := tenantdb.SystemTx(ctx, db, func(tx *gorm.DB) error {
				got = bodies(t, tenantdb.DB(gormctx.New(ctx, tx), db))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return got
		}, []string{"note of " + orgA, "note of " + orgB}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.read(t); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("visible notes = %q, want %q", got, tt.want)
			}
		})
	}
}
