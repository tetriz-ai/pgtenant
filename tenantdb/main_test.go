// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb_test

import (
	"context"
	"os"
	"testing"

	"github.com/peterldowns/pgtestdb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tetriz-ai/pgtenant/internal/testpg"
	"github.com/tetriz-ai/pgtenant/tenantdb"
	"github.com/tetriz-ai/pgtenant/testdb"
)

// dbErr is set when the test server is unreachable. Tests that need a
// database skip on it; the unit tests in this package still run.
var dbErr error

func TestMain(m *testing.M) {
	dbErr = testpg.Check("tenantdb database")
	os.Exit(m.Run())
}

// Two fixed tenants, so failures print readable ids.
const (
	orgA = "00000000-0000-4000-8000-00000000000a"
	orgB = "00000000-0000-4000-8000-00000000000b"
)

// forceRLS makes the table owner, which the test connection is, subject to the
// notes policies. Without it the owner bypasses row-level security.
var forceRLS = testdb.WithPostHook("ALTER TABLE notes FORCE ROW LEVEL SECURITY")

// newGormDB returns a *gorm.DB on a fresh database cloned from the sample
// snapshot in ../testdata.
func newGormDB(t *testing.T, opts ...testdb.Option) *gorm.DB {
	t.Helper()
	if dbErr != nil {
		t.Skipf("no test database: %v", dbErr)
	}
	sqlDB := pgtestdb.New(t, testpg.Config(), testdb.NewMigrator(os.DirFS("../testdata"), "schema.sql", opts...))
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	return db
}

// seed inserts one organization per id and one note per organization, in
// system scope. The note body is "note of <org>".
func seed(t *testing.T, db *gorm.DB, orgs ...string) {
	t.Helper()
	err := tenantdb.SystemTx(context.Background(), db, func(tx *gorm.DB) error {
		for _, org := range orgs {
			if err := tx.Exec("INSERT INTO organizations (id, name) VALUES (?, ?)", org, "org "+org).Error; err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, ?)", org, "note of "+org).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// bodies returns the note bodies visible through tx, sorted.
func bodies(t *testing.T, tx *gorm.DB) []string {
	t.Helper()
	var out []string
	if err := tx.Raw("SELECT body FROM notes ORDER BY body").Scan(&out).Error; err != nil {
		t.Fatalf("select notes: %v", err)
	}
	return out
}
