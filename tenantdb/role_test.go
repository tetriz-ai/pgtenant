// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/tetriz-ai/pgtenant/internal/testpg"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// TestRoleThatBypassesRLS: an enforced scope refuses a role that row-level
// security does not apply to, which would see every tenant's rows. The role in
// effect is what counts, so a superuser session that ran SET ROLE to the app
// role is filtered like the app role. System scope sees every row anyway, so a
// bypassing role may run it.
func TestRoleThatBypassesRLS(t *testing.T) {
	app := newGormDB(t, forceRLS)
	seed(t, app, orgA, orgB)
	var dbName, appRole string
	if err := app.Raw("SELECT current_database(), current_user").Row().Scan(&dbName, &appRole); err != nil {
		t.Fatal(err)
	}
	superuser := openAs(t, testpg.Config().User, testpg.Config().Password, dbName)
	for _, stmt := range []string{
		`DO $$ BEGIN
			CREATE ROLE pgtenant_bypassrls LOGIN PASSWORD 'bypass' NOSUPERUSER BYPASSRLS;
		EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
		END $$`,
		"GRANT SELECT ON notes TO pgtenant_bypassrls",
	} {
		if err := superuser.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	bypassRLS := openAs(t, "pgtenant_bypassrls", "bypass", dbName)
	// A caller's naming strategy must not change what the role check reads.
	upperNames := openAs(t, testpg.Config().User, testpg.Config().Password, dbName)
	upperNames.NamingStrategy = schema.NamingStrategy{NoLowerCase: true}
	// One connection, so the session-level SET ROLE holds for every query.
	setRole := openAs(t, testpg.Config().User, testpg.Config().Password, dbName)
	if sqlDB, err := setRole.DB(); err != nil {
		t.Fatal(err)
	} else {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := setRole.Exec("SET ROLE " + appRole).Error; err != nil {
		t.Fatal(err)
	}

	tenantTx := func(t *testing.T, db *gorm.DB) ([]string, error) {
		var got []string
		err := tenantdb.TenantTx(context.Background(), db, orgA, func(tx *gorm.DB) error {
			got = bodies(t, tx)
			return nil
		})
		return got, err
	}
	inScope := func(_ *testing.T, db *gorm.DB) ([]string, error) {
		return tenantdb.RunInScopeValue(context.Background(), orgA, tenantdb.ScopeEnforced, func(ctx context.Context) ([]string, error) {
			var got []string
			err := tenantdb.DB(ctx, db).Raw("SELECT body FROM notes ORDER BY body").Scan(&got).Error
			return got, err
		})
	}
	systemTx := func(t *testing.T, db *gorm.DB) ([]string, error) {
		var got []string
		err := tenantdb.SystemTx(context.Background(), db, func(tx *gorm.DB) error {
			got = bodies(t, tx)
			return nil
		})
		return got, err
	}
	onlyA := []string{"note of " + orgA}
	tests := []struct {
		name    string
		db      *gorm.DB
		run     func(*testing.T, *gorm.DB) ([]string, error)
		want    []string
		wantErr error
	}{
		{"the app role, TenantTx", app, tenantTx, onlyA, nil},
		{"the app role, a scope through DB", app, inScope, onlyA, nil},
		{"a superuser, TenantTx", superuser, tenantTx, nil, tenantdb.ErrRoleBypassesRLS},
		{"a superuser with its own naming strategy", upperNames, tenantTx, nil, tenantdb.ErrRoleBypassesRLS},
		{"a superuser, a scope through DB", superuser, inScope, nil, tenantdb.ErrRoleBypassesRLS},
		{"a BYPASSRLS role, TenantTx", bypassRLS, tenantTx, nil, tenantdb.ErrRoleBypassesRLS},
		{"a BYPASSRLS role, a scope through DB", bypassRLS, inScope, nil, tenantdb.ErrRoleBypassesRLS},
		{"a superuser after SET ROLE to the app role", setRole, tenantTx, onlyA, nil},
		{"a superuser, SystemTx", superuser, systemTx, []string{"note of " + orgA, "note of " + orgB}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.run(t, tt.db)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v with visible notes %q, want %v", err, got, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("visible notes = %q, want %q", got, tt.want)
			}
		})
	}
}

// openAs connects to the test database dbName as user.
func openAs(t *testing.T, user, password, dbName string) *gorm.DB {
	t.Helper()
	conf := testpg.Config()
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		conf.Host, conf.Port, user, password, dbName)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("connect as %s: %v", user, err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
