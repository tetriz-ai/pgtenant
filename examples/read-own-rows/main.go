// Command read-own-rows reads notes inside tenant A's scope.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/internal/demo"
	"github.com/tetriz-ai/pgtenant/tenantdb"
)

func main() {
	ctx := context.Background()
	db, err := demo.Setup(ctx) // two tenants' notes behind a row-level security policy
	if err != nil {
		log.Fatal(err)
	}
	err = tenantdb.TenantTx(ctx, db, demo.TenantA, func(tx *gorm.DB) error {
		var bodies []string
		if err := tx.Raw("SELECT body FROM notes ORDER BY body").Scan(&bodies).Error; err != nil {
			return err
		}
		fmt.Println(strings.Join(bodies, " | "))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
