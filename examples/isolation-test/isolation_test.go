// Package isolation is a test to copy into your own repository: each test gets
// a fresh database cloned from your committed schema snapshot, and proves that
// tenant A cannot read or write tenant B's rows.
package isolation

import (
	"context"
	"fmt"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/peterldowns/pgtestdb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tetriz-ai/pgtenant/tenantdb"
	"github.com/tetriz-ai/pgtenant/testdb"
)

const (
	tenantA = "00000000-0000-4000-8000-00000000000a"
	tenantB = "00000000-0000-4000-8000-00000000000b"
)

// The compose Postgres on localhost:5433, or PGTENANT_TEST_HOST and
// PGTENANT_TEST_PORT. pgtestdb creates its own role and databases on it.
var conf = pgtestdb.Config{
	DriverName: "pgx",
	Host:       env("PGTENANT_TEST_HOST", "localhost"),
	Port:       env("PGTENANT_TEST_PORT", "5433"),
	User:       "postgres",
	Password:   "password",
	Database:   "postgres",
	Options:    "sslmode=disable",
}

// The snapshot pg_dump wrote. The test role owns the tables, so
// FORCE makes the policies apply to it.
var migrator = testdb.NewMigrator(os.DirFS("../../testdata"), "schema.sql",
	testdb.WithPostHook("ALTER TABLE notes FORCE ROW LEVEL SECURITY"))

func TestMain(m *testing.M) {
	if err := testdb.Ping(context.Background(), conf); err != nil {
		testdb.SkipOrFail("isolation", err) // exits 1 when PGTENANT_REQUIRE_TEST_DB=1
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func open(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pgtestdb.New(t, conf, migrator)}),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTenantACannotSeeTenantB(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	if err := tenantdb.SystemTx(ctx, db, func(tx *gorm.DB) error {
		for _, org := range []string{tenantA, tenantB} {
			if err := tx.Exec("INSERT INTO organizations (id, name) VALUES (?, ?)", org, org).Error; err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, ?)", org, "note of "+org).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		tenant  string
		wantOrg string
	}{
		{"tenant A sees only A", tenantA, tenantA},
		{"tenant B sees only B", tenantB, tenantB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var orgs []string
			err := tenantdb.TenantTx(ctx, db, tt.tenant, func(tx *gorm.DB) error {
				return tx.Raw("SELECT organization_id::text FROM notes").Scan(&orgs).Error
			})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(orgs) != fmt.Sprint([]string{tt.wantOrg}) {
				t.Fatalf("visible organizations = %v, want only %s", orgs, tt.wantOrg)
			}
		})
	}

	t.Run("tenant A cannot write a tenant B row", func(t *testing.T) {
		err := tenantdb.TenantTx(ctx, db, tenantA, func(tx *gorm.DB) error {
			return tx.Exec("INSERT INTO notes (organization_id, body) VALUES (?, 'sneaky')", tenantB).Error
		})
		if err == nil {
			t.Fatal("the write succeeded; want a row-level security violation")
		}
	})
}
