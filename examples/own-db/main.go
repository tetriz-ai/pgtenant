// Command own-db reads tenant A's notes from the database in DATABASE_URL.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/tetriz-ai/pgtenant/tenantdb"
)

func main() {
	db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	const tenantA = "00000000-0000-4000-8000-00000000000a"
	err = tenantdb.TenantTx(context.Background(), db, tenantA, func(tx *gorm.DB) error {
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
