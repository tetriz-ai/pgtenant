// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package gormctx carries a *gorm.DB transaction and a row-lock flag in a
// context.Context, so DAO methods can join a caller's transaction.
package gormctx

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type key string

const (
	txKey   key = "gormctx_tx"
	lockKey key = "gormctx_lock"
)

var forUpdate = clause.Locking{Strength: "UPDATE"}

// New returns a context carrying db, usually a transaction. A nil db leaves
// ctx unchanged.
func New(ctx context.Context, db *gorm.DB) context.Context {
	if db == nil {
		return ctx
	}
	return context.WithValue(ctx, txKey, db)
}

// FromContext returns the *gorm.DB that New stored in ctx, and whether there
// was one.
func FromContext(ctx context.Context) (*gorm.DB, bool) {
	db, ok := ctx.Value(txKey).(*gorm.DB)
	return db, ok && db != nil
}

// FromContextOrDefault returns the *gorm.DB in ctx, or defaultDB when there is
// none, bound to ctx. With the lock flag from NewWithLockKey in ctx, its
// SELECTs lock rows with FOR UPDATE. It returns nil when both are nil.
func FromContextOrDefault(ctx context.Context, defaultDB *gorm.DB) *gorm.DB {
	db := defaultDB
	if carried, ok := FromContext(ctx); ok {
		db = carried
	}
	if db == nil {
		return nil
	}
	if locked, _ := ctx.Value(lockKey).(bool); locked {
		db = db.Clauses(forUpdate)
	}
	if db.Statement == nil || db.Statement.Context != ctx {
		db = db.WithContext(ctx)
	}
	return db
}

// NewWithLock returns a context carrying the *gorm.DB from parent, or
// defaultDB, with FOR UPDATE on its SELECTs. With neither it returns parent.
func NewWithLock(parent context.Context, defaultDB *gorm.DB) context.Context {
	db := FromContextOrDefault(parent, defaultDB)
	if db == nil {
		return parent
	}
	return New(parent, db.Clauses(forUpdate))
}

// NewWithLockKey returns a context carrying the lock flag, so every handle
// FromContextOrDefault returns for it adds FOR UPDATE.
func NewWithLockKey(parent context.Context) context.Context {
	return context.WithValue(parent, lockKey, true)
}

// FromContextWithLock returns the *gorm.DB in ctx with FOR UPDATE on its
// SELECTs, and whether there was one.
func FromContextWithLock(ctx context.Context) (*gorm.DB, bool) {
	db, ok := FromContext(ctx)
	if !ok {
		return nil, false
	}
	return db.Clauses(forUpdate), true
}
