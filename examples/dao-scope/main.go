// Command dao-scope runs one DAO method inside tenant B's scope, then outside any scope.
package main

import (
	"context"
	"fmt"
	"log"

	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// countNotes is a DAO method. It takes no tenant, because the scope in ctx picks one.
func countNotes(ctx context.Context, db *gorm.DB) (int64, error) {
	var n int64
	err := tenantdb.DB(ctx, db).Raw("SELECT count(*) FROM notes").Scan(&n).Error
	return n, err
}

func main() {
	ctx := context.Background()
	db, err := demo.Setup(ctx)
	if err != nil {
		log.Fatal(err)
	}
	n, err := tenantdb.RunInScopeValue(ctx, demo.TenantB, tenantdb.ScopeEnforced,
		func(ctx context.Context) (int64, error) { return countNotes(ctx, db) })
	fmt.Println("in tenant B's scope:", n, err)
	_, err = countNotes(ctx, db)
	fmt.Println("outside any scope:", err)
}
