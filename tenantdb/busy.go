// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync"

	"gorm.io/gorm"
)

// oneAtATime is the connection pool of a scope's transaction. A transaction is
// one connection, and a Postgres connection runs one statement at a time: a
// statement sent while another runs or has rows open breaks the connection, and
// pgx can panic. A context, and the scope in it, is easily handed to several
// goroutines, so a statement that would overlap another fails with
// ErrScopeBusy before it reaches the connection.
type oneAtATime struct {
	gorm.Tx
	mu      sync.Mutex
	running bool      // a statement is executing
	rows    *sql.Rows // the last query's rows, which hold the connection until closed
}

// oneStatementAtATime routes the statements of tx, a scope's new transaction,
// through a oneAtATime. Every handle DB later derives from tx shares it.
func oneStatementAtATime(tx *gorm.DB) {
	if inner, ok := tx.Statement.ConnPool.(gorm.Tx); ok {
		tx.Statement.ConnPool = &oneAtATime{Tx: inner}
	}
}

// claim takes the connection for one statement, or returns ErrScopeBusy.
func (p *oneAtATime) claim() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return ErrScopeBusy
	}
	if p.rows != nil {
		// Columns fails once the rows are closed, which Next does at the end.
		if _, err := p.rows.Columns(); err == nil {
			return ErrScopeBusy
		}
	}
	p.running, p.rows = true, nil
	return nil
}

// release gives the connection back. rows, when not nil, keep it until they
// are closed.
func (p *oneAtATime) release(rows *sql.Rows) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running, p.rows = false, rows
}

func (p *oneAtATime) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := p.claim(); err != nil {
		return nil, err
	}
	defer p.release(nil)
	return p.Tx.ExecContext(ctx, query, args...)
}

func (p *oneAtATime) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if err := p.claim(); err != nil {
		return nil, err
	}
	defer p.release(nil)
	return p.Tx.PrepareContext(ctx, query)
}

func (p *oneAtATime) QueryContext(ctx context.Context, query string, args ...any) (rows *sql.Rows, err error) {
	if err := p.claim(); err != nil {
		return nil, err
	}
	defer func() { p.release(rows) }()
	return p.Tx.QueryContext(ctx, query, args...)
}

// QueryRowContext is what gorm's Row calls. The *sql.Row it returns holds its
// rows open until Scan, but gives this pool no way to see when that happens,
// so the connection counts as free once the call returns. A statement that
// starts between Row and Scan reaches the driver, which fails it with its own
// error.
func (p *oneAtATime) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if err := p.claim(); err != nil {
		return busyDB.QueryRowContext(ctx, "")
	}
	defer p.release(nil)
	return p.Tx.QueryRowContext(ctx, query, args...)
}

// busyDB is a database whose every connection attempt fails with
// ErrScopeBusy, the one way to build a *sql.Row that carries that error.
var busyDB = sql.OpenDB(busyConnector{})

type busyConnector struct{}

func (busyConnector) Connect(context.Context) (driver.Conn, error) { return nil, ErrScopeBusy }
func (busyConnector) Driver() driver.Driver                        { return busyConnector{} }
func (busyConnector) Open(string) (driver.Conn, error)             { return nil, ErrScopeBusy }
