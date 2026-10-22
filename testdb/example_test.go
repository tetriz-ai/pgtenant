// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testdb_test

import (
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/peterldowns/pgtestdb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/testdb"
)

// conf is the test Postgres, here the compose one on localhost:5433. pgtestdb
// creates its own role and databases on it.
var conf = pgtestdb.Config{
	DriverName: "pgx",
	Host:       "localhost",
	Port:       "5433",
	User:       "postgres",
	Password:   "password",
	Database:   "postgres",
	Options:    "sslmode=disable",
}

// migrator loads the committed schema dump into each test's database. The test
// role owns the tables, so FORCE makes the policies apply to it.
var migrator = testdb.NewMigrator(os.DirFS("testdata"), "schema.sql",
	testdb.WithPostHook("ALTER TABLE notes FORCE ROW LEVEL SECURITY"))

// openDB gives a test its own database, cloned from a template built once
// from the dump.
func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pgtestdb.New(t, conf, migrator)}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// Each test calls openDB for a fresh database with your schema and policies.
// examples/isolation-test is a complete test file built this way.
func ExampleNewMigrator() {
	_ = openDB
}
