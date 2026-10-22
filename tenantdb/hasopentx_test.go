// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb_test

import (
	"context"
	"testing"

	"github.com/tetriz-ai/pgtenant/tenantdb"
)

// TestHasOpenTx: true only between a scope's first query and its finish.
func TestHasOpenTx(t *testing.T) {
	db := newGormDB(t)
	ctx := tenantdb.NewContext(context.Background(), orgA, tenantdb.ScopeEnforced)
	steps := []struct {
		name string
		do   func()
		want bool
	}{
		{"before any query", func() {}, false},
		{"after the first query", func() { tenantdb.DB(ctx, db).Exec("SELECT 1") }, true},
		{"after CommitOrRollback", func() { _ = tenantdb.CommitOrRollback(ctx, nil) }, false},
	}
	for _, st := range steps {
		st.do()
		if got := tenantdb.HasOpenTx(ctx); got != st.want {
			t.Fatalf("%s: HasOpenTx = %v, want %v", st.name, got, st.want)
		}
	}
}
