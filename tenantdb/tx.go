// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package tenantdb

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// TenantTx runs fn in a transaction for orgID: BEGIN, set
// app.current_tenant for the transaction (and app.data_cutoff from
// WithDataCutoff), fn, then COMMIT, or ROLLBACK when fn returns an error or
// panics. fn's error is returned unwrapped. The org ID and cutoff are checked
// before BEGIN. Under a role that bypasses row-level security it returns
// ErrRoleBypassesRLS before fn runs. It ignores any scope from NewContext.
func TenantTx(ctx context.Context, db *gorm.DB, orgID string, fn func(tx *gorm.DB) error) error {
	if orgID == "" {
		return fmt.Errorf("%w: org_id must not be empty", ErrInvalidOrgID)
	}
	set, err := settingsFor(ctx, orgID, ScopeEnforced)
	if err != nil {
		return err
	}
	return runTx(ctx, db, set, fn)
}

// SystemTx is TenantTx for every tenant: it writes SystemTenantID to
// app.current_tenant and -infinity to app.data_cutoff. It sees every row
// anyway, so it also runs under a role that bypasses row-level security.
func SystemTx(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	set, err := settingsFor(ctx, "", ScopeSystem)
	if err != nil {
		return err
	}
	return runTx(ctx, db, set, fn)
}

func runTx(ctx context.Context, db *gorm.DB, set settings, fn func(tx *gorm.DB) error) error {
	if db == nil {
		return ErrNilDB
	}
	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("tenantdb: begin: %w", tx.Error)
	}
	// Roll back on every path that does not reach the commit: an error, a
	// panic (which keeps propagating) or runtime.Goexit from t.Fatal in fn.
	committing := false
	defer func() {
		if !committing {
			tx.Rollback()
		}
	}()
	if err := set.apply(tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	committing = true
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("tenantdb: commit: %w", err)
	}
	return nil
}
