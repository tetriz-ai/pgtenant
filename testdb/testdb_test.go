// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testdb_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/peterldowns/pgtestdb"

	"github.com/tetriz-ai/pgtenant/internal/testpg"
	"github.com/tetriz-ai/pgtenant/testdb"
)

var dbErr error

func TestMain(m *testing.M) {
	// The TestSkipOrFail helper subprocess exercises SkipOrFail itself, so the
	// package-level ping must not exit it first.
	if os.Getenv("PGTENANT_TESTDB_HELPER") == "1" {
		os.Exit(m.Run())
	}
	dbErr = testpg.Check("testdb database")
	os.Exit(m.Run())
}

var snapshot = os.DirFS("../testdata")

func newDB(t *testing.T, m pgtestdb.Migrator) *sql.DB {
	t.Helper()
	if dbErr != nil {
		t.Skipf("no test database: %v", dbErr)
	}
	return pgtestdb.New(t, testpg.Config(), m)
}

// TestHash pins the template identity: equal only for the same snapshot bytes
// and the same options in the same order.
func TestHash(t *testing.T) {
	hash := func(t *testing.T, fsys fstest.MapFS, opts ...testdb.Option) string {
		t.Helper()
		h, err := testdb.NewMigrator(fsys, "s.sql", opts...).Hash()
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		return h
	}
	a := fstest.MapFS{"s.sql": {Data: []byte("CREATE TABLE a (id int);")}}
	b := fstest.MapFS{"s.sql": {Data: []byte("CREATE TABLE b (id int);")}}
	base := hash(t, a)
	tests := []struct {
		name      string
		got       string
		wantEqual bool
	}{
		{"same snapshot, no options", hash(t, a), true},
		{"different snapshot", hash(t, b), false},
		{"an added option", hash(t, a, testdb.WithDropIndex("i")), false},
		{"the same value under another option", hash(t, a, testdb.WithPostHook("i")), false},
		{"one value split in two", hash(t, a, testdb.WithPostHook("a", "b")), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.got == base) != tt.wantEqual {
				t.Fatalf("hash %s vs base %s: wantEqual=%v", tt.got, base, tt.wantEqual)
			}
		})
	}
	t.Run("the same value under two option kinds differs", func(t *testing.T) {
		if hash(t, a, testdb.WithDropIndex("i")) == hash(t, a, testdb.WithPostHook("i")) {
			t.Fatal(`WithDropIndex("i") and WithPostHook("i") share a hash`)
		}
	})
	t.Run("one value and the same text split in two differ", func(t *testing.T) {
		if hash(t, a, testdb.WithPostHook("ab")) == hash(t, a, testdb.WithPostHook("a", "b")) {
			t.Fatal(`WithPostHook("ab") and WithPostHook("a", "b") share a hash`)
		}
	})
	t.Run("options of different kinds in another order share a hash", func(t *testing.T) {
		if hash(t, a, testdb.WithDropIndex("i"), testdb.WithPostHook("x")) != hash(t, a, testdb.WithPostHook("x"), testdb.WithDropIndex("i")) {
			t.Fatal("reordering options of different kinds changed the hash")
		}
	})
	t.Run("options in another order differ", func(t *testing.T) {
		if hash(t, a, testdb.WithPostHook("x"), testdb.WithPostHook("y")) == hash(t, a, testdb.WithPostHook("y"), testdb.WithPostHook("x")) {
			t.Fatal("reordered post hooks share a hash")
		}
	})
	t.Run("a missing snapshot is an error", func(t *testing.T) {
		if _, err := testdb.NewMigrator(a, "missing.sql").Hash(); err == nil {
			t.Fatal("want an error")
		}
	})
}

// TestMigratorAppliesOptions loads the sample snapshot with each option and
// checks its effect in the cloned database.
func TestMigratorAppliesOptions(t *testing.T) {
	db := newDB(t, testdb.NewMigrator(snapshot, "schema.sql",
		testdb.WithStubTable("CREATE TABLE people (id uuid PRIMARY KEY)"),
		testdb.WithDefaultPartition("note_events"),
		testdb.WithDropIndex("notes_one_pinned_per_org"),
		testdb.WithPostHook("ALTER TABLE notes FORCE ROW LEVEL SECURITY"),
	))
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"snapshot tables exist", "SELECT to_regclass('public.notes') IS NOT NULL", true},
		{"stub table exists", "SELECT to_regclass('public.people') IS NOT NULL", true},
		{"default partition exists", "SELECT to_regclass('public.note_events_default') IS NOT NULL", true},
		{"the dump's own partition loads too", "SELECT to_regclass('public.note_events_2026') IS NOT NULL", true},
		{"dropped index is gone", "SELECT to_regclass('public.notes_one_pinned_per_org') IS NOT NULL", false},
		{"post hook ran", "SELECT relforcerowsecurity FROM pg_class WHERE oid = 'public.notes'::regclass", true},
		{"policies came from the snapshot", "SELECT count(*) = 2 FROM pg_policies WHERE tablename = 'notes'", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bool
			if err := db.QueryRow(tt.query).Scan(&got); err != nil {
				t.Fatalf("query: %v", err)
			}
			if got != tt.want {
				t.Fatalf("%s = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
	t.Run("the default partition accepts any date", func(t *testing.T) {
		if _, err := db.Exec("INSERT INTO note_events VALUES (gen_random_uuid(), '1999-01-01', 'created')"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	})
}

// TestLoadSchemaFailures pins that a bad snapshot or option is an error and
// leaves the previous schema in place, because the load is one transaction.
func TestLoadSchemaFailures(t *testing.T) {
	db := newDB(t, testdb.NewMigrator(snapshot, "schema.sql"))
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		opts    []testdb.Option
		wantErr string
	}{
		{"invalid SQL", fstest.MapFS{"s.sql": {Data: []byte("CREATE TABLE broken (")}}, nil, "load s.sql"},
		{"missing file", fstest.MapFS{}, nil, "read schema"},
		{"partition parent that is not an identifier", fstest.MapFS{"s.sql": {Data: []byte("SELECT 1;")}}, []testdb.Option{testdb.WithDefaultPartition("notes; DROP TABLE x")}, "not a valid identifier"},
		{"index name that is not an identifier", fstest.MapFS{"s.sql": {Data: []byte("SELECT 1;")}}, []testdb.Option{testdb.WithDropIndex("1idx")}, "not a valid identifier"},
		{"failing post hook", fstest.MapFS{"s.sql": {Data: []byte("SELECT 1;")}}, []testdb.Option{testdb.WithPostHook("SELECT * FROM nowhere")}, "post hook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testdb.LoadSchema(context.Background(), db, tt.fsys, "s.sql", tt.opts...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
			var intact bool
			if err := db.QueryRow("SELECT to_regclass('public.notes') IS NOT NULL").Scan(&intact); err != nil || !intact {
				t.Fatalf("previous schema not intact after a failed load (err %v)", err)
			}
		})
	}
}

// TestPingUnreachable pins that Ping reports an unreachable server as an error.
func TestPingUnreachable(t *testing.T) {
	conf := testpg.Config()
	conf.Port = "1"
	if err := testdb.Ping(context.Background(), conf); err == nil {
		t.Fatal("want an error for a closed port")
	}
}

// TestIdentifiersWithDigits pins that option names may contain digits after
// the first character.
func TestIdentifiersWithDigits(t *testing.T) {
	db := newDB(t, testdb.NewMigrator(snapshot, "schema.sql"))
	fsys := fstest.MapFS{"s.sql": {Data: []byte("CREATE TABLE t2 (id int); CREATE INDEX idx2 ON t2 (id);")}}
	if err := testdb.LoadSchema(context.Background(), db, fsys, "s.sql", testdb.WithDropIndex("idx2")); err != nil {
		t.Fatalf("LoadSchema with WithDropIndex(\"idx2\"): %v", err)
	}
	var present bool
	if err := db.QueryRow("SELECT to_regclass('public.idx2') IS NOT NULL").Scan(&present); err != nil || present {
		t.Fatalf("idx2 present = %v (err %v), want dropped", present, err)
	}
}

// TestIdentifierRules pins which option names LoadSchema accepts: letters,
// digits after the first character and underscores, nothing else.
func TestIdentifierRules(t *testing.T) {
	db := newDB(t, testdb.NewMigrator(snapshot, "schema.sql"))
	fsys := fstest.MapFS{"s.sql": {Data: []byte("CREATE TABLE t9 (id int); CREATE INDEX idx9 ON t9 (id); CREATE INDEX _x ON t9 (id);")}}
	tests := []struct {
		name    string
		index   string
		wantErr bool
	}{
		{"a trailing 9", "idx9", false},
		{"a leading underscore", "_x", false},
		{"a dot", "a.b", true},
		{"a leading digit", "9idx", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testdb.LoadSchema(context.Background(), db, fsys, "s.sql", testdb.WithDropIndex(tt.index))
			if (err != nil) != tt.wantErr {
				t.Fatalf("WithDropIndex(%q): err = %v, wantErr %v", tt.index, err, tt.wantErr)
			}
		})
	}
}

// TestOptionOrder pins the documented order: stub tables, then default
// partitions, then dropped indexes, then post hooks.
func TestOptionOrder(t *testing.T) {
	db := newDB(t, testdb.NewMigrator(snapshot, "schema.sql"))
	fsys := fstest.MapFS{"s.sql": {Data: []byte("CREATE TABLE t (id int); CREATE INDEX t_idx ON t (id);")}}
	err := testdb.LoadSchema(context.Background(), db, fsys, "s.sql",
		// The partition needs the stub table, so stubs must run first.
		testdb.WithStubTable("CREATE TABLE events (d date) PARTITION BY RANGE (d)"),
		testdb.WithDefaultPartition("events"),
		// The hook recreates the dropped index, so it must run after the drop.
		testdb.WithDropIndex("t_idx"),
		testdb.WithPostHook("CREATE INDEX t_idx ON t (id)"),
	)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	var partition, index bool
	if err := db.QueryRow("SELECT to_regclass('public.events_default') IS NOT NULL, to_regclass('public.t_idx') IS NOT NULL").Scan(&partition, &index); err != nil {
		t.Fatal(err)
	}
	if !partition || !index {
		t.Fatalf("default partition %v, index recreated by the hook %v; want both", partition, index)
	}
}

// TestLoadSchemaTakesPlainPgDump: a plain pg_dump script loads, and its
// header settings do not stay on the connection.
func TestLoadSchemaTakesPlainPgDump(t *testing.T) {
	db := newDB(t, testdb.NewMigrator(snapshot, "schema.sql"))
	db.SetMaxOpenConns(1)
	dump := "\\restrict abc\nSET row_security = off;\nSELECT pg_catalog.set_config('search_path', '', false);\nCREATE TABLE public.plain (id int);\n\\unrestrict abc\n"
	fsys := fstest.MapFS{"s.sql": {Data: []byte(dump)}}
	if err := testdb.LoadSchema(context.Background(), db, fsys, "s.sql", testdb.WithStubTable("CREATE TABLE stub (id int)")); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	var searchPath, rowSecurity string
	if err := db.QueryRow("SELECT current_setting('search_path'), current_setting('row_security')").Scan(&searchPath, &rowSecurity); err != nil {
		t.Fatal(err)
	}
	if searchPath == "" || rowSecurity != "on" {
		t.Fatalf("search_path %q, row_security %q left from the dump", searchPath, rowSecurity)
	}
}
