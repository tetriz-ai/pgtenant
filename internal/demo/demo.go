// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package demo resets the schema the examples share and connects to it as the
// application role. It needs the compose Postgres on 127.0.0.1:5433
// (PGTENANT_TEST_HOST and PGTENANT_TEST_PORT override the address).
package demo

import (
	"context"
	_ "embed" // setup.sql
	"fmt"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// The two tenants in setup.sql.
const (
	TenantA = "00000000-0000-4000-8000-00000000000a"
	TenantB = "00000000-0000-4000-8000-00000000000b"
)

//go:embed setup.sql
var setupSQL string

func addr() string {
	host, port := os.Getenv("PGTENANT_TEST_HOST"), os.Getenv("PGTENANT_TEST_PORT")
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "5433"
	}
	return host + ":" + port
}

// SuperuserDSN connects as the compose superuser, which bypasses every policy.
func SuperuserDSN() string {
	return "postgres://postgres:password@" + addr() + "/postgres?sslmode=disable&search_path=pgtenant_example"
}

// AppDSN connects as pgtenant_app, the role the policies apply to.
func AppDSN() string {
	return "postgres://pgtenant_app:app@" + addr() + "/postgres?sslmode=disable&search_path=pgtenant_example"
}

// Open connects gorm to dsn with logging off.
func Open(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect to %s (is the compose Postgres running?): %w", addr(), err)
	}
	return db, nil
}

// Setup runs setup.sql as the superuser, then returns a handle connected as
// pgtenant_app. It retries for up to 30 seconds, so it works right after the
// compose Postgres starts.
func Setup(ctx context.Context) (*gorm.DB, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := setup(ctx)
		if err == nil {
			return Open(AppDSN())
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func setup(ctx context.Context) error {
	admin, err := Open(SuperuserDSN())
	if err != nil {
		return err
	}
	defer func() {
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := admin.WithContext(ctx).Exec(setupSQL).Error; err != nil {
		return fmt.Errorf("run setup.sql: %w", err)
	}
	return nil
}
