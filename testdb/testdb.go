// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package testdb loads a pg_dump schema snapshot into test databases, so tests
// run against the schema you ship, row-level security policies included.
//
// [Migrator] plugs the snapshot into github.com/peterldowns/pgtestdb, which
// creates one template database per snapshot and clones it for each test.
package testdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"strings"

	"github.com/peterldowns/pgtestdb"
)

// Option configures LoadSchema. Options add what a schema-only dump leaves out.
// That includes default partitions, stub tables another database owns, RLS
// settings, and index drops for parallel tests.
type Option func(*config)

type config struct {
	defaultPartitions []string
	stubTables        []string
	postHooks         []string
	dropIndexes       []string
}

// WithDefaultPartition declares a DEFAULT partition for a partitioned parent.
// A schema-only dump carries the partitions the source database has, so a row
// outside their ranges has nowhere to go. The DEFAULT partition accepts it.
func WithDefaultPartition(parents ...string) Option {
	return func(c *config) { c.defaultPartitions = append(c.defaultPartitions, parents...) }
}

// WithStubTable runs an inline CREATE TABLE for a table the snapshot
// references but does not define.
func WithStubTable(ddl ...string) Option {
	return func(c *config) { c.stubTables = append(c.stubTables, ddl...) }
}

// WithDropIndex DROPs an index after loading the schema. Use it for a unique
// index that parallel tests inserting the same rows would violate.
func WithDropIndex(name ...string) Option {
	return func(c *config) { c.dropIndexes = append(c.dropIndexes, name...) }
}

// WithPostHook runs arbitrary SQL after the schema load. Use sparingly — FORCE
// ROW LEVEL SECURITY, GRANTs, and other test-only setup belong here.
func WithPostHook(sql ...string) Option {
	return func(c *config) { c.postHooks = append(c.postHooks, sql...) }
}

// Migrator is a [pgtestdb.Migrator] that loads a schema snapshot from fsys at
// path, then applies the options. pgtestdb calls Hash to name the template
// database and Migrate once per template.
type Migrator struct {
	fsys fs.FS
	path string
	opts []Option
}

var _ pgtestdb.Migrator = (*Migrator)(nil)

// NewMigrator returns a Migrator for the snapshot at path in fsys.
func NewMigrator(fsys fs.FS, path string, opts ...Option) *Migrator {
	return &Migrator{fsys: fsys, path: path, opts: opts}
}

// Hash returns a content hash of the snapshot and the applied options, so two
// tests share a template only when their schema would be byte-equal. Any edit to
// the snapshot, committed or not, produces a new template.
func (m *Migrator) Hash() (string, error) {
	schema, err := fs.ReadFile(m.fsys, m.path)
	if err != nil {
		return "", fmt.Errorf("hash schema %s: %w", m.path, err)
	}
	cfg := &config{}
	for _, opt := range m.opts {
		opt(cfg)
	}
	h := sha256.New()
	_, _ = h.Write(schema)
	writeHashedSlice(h, "stub", cfg.stubTables)
	writeHashedSlice(h, "part", cfg.defaultPartitions)
	writeHashedSlice(h, "drop", cfg.dropIndexes)
	writeHashedSlice(h, "hook", cfg.postHooks)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeHashedSlice(h hash.Hash, tag string, vals []string) {
	_, _ = h.Write([]byte("\x00" + tag + "\x00"))
	for _, v := range vals {
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte{0})
	}
}

// Migrate loads the snapshot into db with [LoadSchema].
func (m *Migrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	return LoadSchema(ctx, db, m.fsys, m.path, m.opts...)
}

// LoadSchema drops the public schema, recreates it, then executes the
// snapshot at path in fsys, as pg_dump --schema-only writes it. Lines that
// start with a backslash are psql meta-commands the server cannot run
// (\restrict and \unrestrict), so they are dropped, and the session settings
// the dump sets are reset afterwards. Then options run in order:
// stub tables -> default partitions -> drop indexes -> post hooks. The
// connection's role must own the database (pgtestdb's test role does).
//
// Everything runs inside one transaction so a failed reload never leaves
// the test DB in a torn half-schema state.
func LoadSchema(ctx context.Context, db *sql.DB, fsys fs.FS, path string, opts ...Option) error {
	schema, err := fs.ReadFile(fsys, path)
	if err != nil {
		return fmt.Errorf("read schema %s: %w", path, err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire pool conn: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := loadSchemaOnConn(ctx, conn, string(schema), path, opts...); err != nil {
		return fmt.Errorf("load schema on conn for %s: %w", path, err)
	}
	return nil
}

func loadSchemaOnConn(ctx context.Context, conn *sql.Conn, schema, path string, opts ...Option) error {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema reload tx for %s: %w", path, err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback() // after a failed step; the step's error is the one returned
		}
	}()

	// DROP SCHEMA CASCADE wipes tables, indexes, sequences, functions, policies.
	// CREATE EXTENSION statements inside the snapshot get re-applied on the fresh schema.
	const resetSQL = `
		DROP SCHEMA IF EXISTS public CASCADE;
		CREATE SCHEMA public;
		GRANT ALL ON SCHEMA public TO PUBLIC;
	`
	if _, err := tx.ExecContext(ctx, resetSQL); err != nil {
		return fmt.Errorf("reset public schema for %s: %w", path, err)
	}

	if _, err := tx.ExecContext(ctx, withoutMetaCommands(schema)); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	// pg_dump's header SETs (an empty search_path, row_security off) would
	// otherwise stay on this connection and break the options below.
	if _, err := tx.ExecContext(ctx, "RESET ALL"); err != nil {
		return fmt.Errorf("reset settings after %s: %w", path, err)
	}

	for _, ddl := range cfg.stubTables {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("stub table for %s: %w", path, err)
		}
	}

	for _, parent := range cfg.defaultPartitions {
		// parent/idx are interpolated unquoted; validate so a typo'd option is a
		// loud error here, not an opaque SQL failure, and not an injection vector.
		if !validIdent(parent) {
			return fmt.Errorf("default partition parent %q for %s is not a valid identifier", parent, path)
		}
		stmt := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_default PARTITION OF %s DEFAULT;`, parent, parent)
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("default partition for %s.%s: %w", path, parent, err)
		}
	}

	for _, idx := range cfg.dropIndexes {
		if !validIdent(idx) {
			return fmt.Errorf("drop index %q for %s is not a valid identifier", idx, path)
		}
		if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS "+idx); err != nil {
			return fmt.Errorf("drop index %s for %s: %w", idx, path, err)
		}
	}

	for _, sqlStmt := range cfg.postHooks {
		if _, err := tx.ExecContext(ctx, sqlStmt); err != nil {
			return fmt.Errorf("post hook for %s: %w", path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema reload for %s: %w", path, err)
	}
	tx = nil
	return nil
}

// validIdent reports whether s is a bare SQL identifier (ASCII letters, digits,
// underscore; no leading digit). Used to gate the unquoted partition-parent and
// index names interpolated into DDL above — callers pass string literals, so a
// false here is a programming error, not user input.
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// withoutMetaCommands drops the lines of a pg_dump script that start with a
// backslash.
func withoutMetaCommands(schema string) string {
	lines := strings.Split(schema, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if !strings.HasPrefix(l, "\\") {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}
