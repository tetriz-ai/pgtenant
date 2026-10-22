// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/gormctx"
)

// TestCanonicalOrgID: a UUID in any spelling uuid.Parse accepts becomes its
// canonical form; anything else, the sentinel included, is refused, and the
// error repeats at most 64 bytes of the input.
func TestCanonicalOrgID(t *testing.T) {
	const canon = "0f8fad5b-d9cb-469f-a165-70867728950e"
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "canonical uuid", in: canon, want: canon},
		{name: "upper case", in: "0F8FAD5B-D9CB-469F-A165-70867728950E", want: canon},
		{name: "urn form", in: "urn:uuid:" + canon, want: canon},
		{name: "braced form", in: "{" + canon + "}", want: canon},
		{name: "32 hex digits", in: "0f8fad5bd9cb469fa16570867728950e", want: canon},
		{name: "the sentinel", in: SystemTenantID, wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "quote injection", in: "x'; DROP TABLE users; --", wantErr: true},
		{name: "fullwidth digit", in: "０f8fad5b-d9cb-469f-a165-70867728950e", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalOrgID(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got (%q, %v), want %q wantErr %v", got, err, tt.want, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidOrgID) {
				t.Fatalf("err = %v, want ErrInvalidOrgID", err)
			}
		})
	}
	t.Run("64 bytes are shown in full, 65 are cut to 64", func(t *testing.T) {
		_, err64 := canonicalOrgID(strings.Repeat("y", 64))
		_, err65 := canonicalOrgID(strings.Repeat("z", 65))
		if !strings.Contains(err64.Error(), `"`+strings.Repeat("y", 64)+`"`) {
			t.Fatalf("64 bytes: %v", err64)
		}
		if !strings.Contains(err65.Error(), `"`+strings.Repeat("z", 64)+`..."`) {
			t.Fatalf("65 bytes: %v", err65)
		}
	})
	t.Run("a long input is cut to 64 bytes in the error", func(t *testing.T) {
		_, err := canonicalOrgID(strings.Repeat("x", 100_000))
		if err == nil || len(err.Error()) > 300 || !strings.Contains(err.Error(), strings.Repeat("x", 64)+"...") {
			t.Fatalf("error of %d bytes: %.120s", len(err.Error()), err)
		}
	})
}

// unreachableDB returns a *gorm.DB whose every connection attempt fails.
func unreachableDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 sslmode=disable connect_timeout=2"}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

// TestValidationBeforeBegin: an invalid org ID or cutoff is reported without a
// round trip, so an unreachable server never shows up as the error.
func TestValidationBeforeBegin(t *testing.T) {
	db := unreachableDB(t)
	noop := func(*gorm.DB) error { return nil }
	tests := []struct {
		name string
		err  func() error
		want error
	}{
		{"TenantTx, empty org", func() error { return TenantTx(context.Background(), db, "", noop) }, ErrInvalidOrgID},
		{"TenantTx, invalid org", func() error { return TenantTx(context.Background(), db, "nope", noop) }, ErrInvalidOrgID},
		{"TenantTx, the sentinel", func() error { return TenantTx(context.Background(), db, SystemTenantID, noop) }, ErrInvalidOrgID},
		{"DB, invalid org", func() error { return DB(NewContext(context.Background(), "nope", ScopeEnforced), db).Error }, ErrInvalidOrgID},
		{"TenantTx, nil db", func() error { return TenantTx(context.Background(), nil, "0f8fad5b-d9cb-469f-a165-70867728950e", noop) }, ErrNilDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.err(); !errors.Is(err, tt.want) || strings.Contains(err.Error(), "begin") {
				t.Fatalf("err = %v, want %v and no begin error", err, tt.want)
			}
		})
	}
	t.Run("TenantTx empty org names the problem", func(t *testing.T) {
		if err := TenantTx(context.Background(), db, "", noop); !strings.Contains(err.Error(), "org_id must not be empty") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestNoScope: without a scope, CommitOrRollback passes err through, HasOpenTx
// is false, DB returns gormctx's transaction when there is one, and otherwise a
// handle carrying ErrNoScope that never reaches the server.
func TestNoScope(t *testing.T) {
	errIn := errors.New("handler error")
	if got := CommitOrRollback(context.Background(), errIn); got != errIn {
		t.Fatalf("CommitOrRollback = %v", got)
	}
	if HasOpenTx(context.Background()) {
		t.Fatal("HasOpenTx without a scope")
	}
	def, carried := unreachableDB(t), unreachableDB(t)
	carried.Statement.ConnPool = &externalTransaction{ConnPool: carried.Statement.ConnPool}
	var n int
	if err := DB(context.Background(), def).Raw("SELECT 1").Scan(&n).Error; !errors.Is(err, ErrNoScope) || strings.Contains(err.Error(), "connect") {
		t.Fatalf("err = %v, want ErrNoScope and no connection attempt", err)
	}
	if err := DB(context.Background(), nil).Error; !errors.Is(err, ErrNoScope) {
		t.Fatalf("nil db: err = %v, want ErrNoScope", err)
	}
	if got := DB(gormctx.New(context.Background(), carried), def); got.Statement.ConnPool != carried.Statement.ConnPool {
		t.Fatal("DB did not return gormctx's handle")
	}
}

func TestDBRejectsCarriedPool(t *testing.T) {
	db := unreachableDB(t)
	for _, locked := range []bool{false, true} {
		ctx := gormctx.New(context.Background(), db)
		if locked {
			ctx = gormctx.NewWithLock(context.Background(), db)
		}
		if err := DB(ctx, db).Error; !errors.Is(err, ErrNoScope) {
			t.Fatalf("locked=%v: got %v, want ErrNoScope", locked, err)
		}
	}
}

func TestFailedHandleCannotBegin(t *testing.T) {
	db := unreachableDB(t)
	handle := DB(context.Background(), db)
	if err := handle.Begin().Error; !errors.Is(err, ErrNoScope) || strings.Contains(err.Error(), "connect") {
		t.Fatalf("Begin = %v, want only ErrNoScope without a connection attempt", err)
	}
}

type beginCounter struct {
	gorm.ConnPool
	begins int
}

func (p *beginCounter) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	p.begins++
	return nil, errors.New("unexpected BEGIN")
}

func TestFailedHandleNeverAcquiresConnection(t *testing.T) {
	for _, operation := range []string{"Begin", "Transaction"} {
		t.Run(operation, func(t *testing.T) {
			db := unreachableDB(t)
			pool := &beginCounter{ConnPool: db.Statement.ConnPool}
			db.Statement.ConnPool = pool
			handle := DB(context.Background(), db)
			switch operation {
			case "Begin":
				_ = handle.Begin()
			case "Transaction":
				_ = handle.Transaction(func(*gorm.DB) error { t.Fatal("callback ran"); return nil })
			}
			if pool.begins != 0 {
				t.Fatalf("failed handle acquired %d connections", pool.begins)
			}
		})
	}
}

type externalTransaction struct{ gorm.ConnPool }

func (*externalTransaction) Commit() error   { return nil }
func (*externalTransaction) Rollback() error { return nil }
