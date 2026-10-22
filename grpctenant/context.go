// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package grpctenant

import "context"

type contextKey string

const ctxOrgID contextKey = "org_id"

// OrgIDFromContext returns the organization ID set by [WithOrgID], or "".
func OrgIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxOrgID).(string)
	return v
}

// WithOrgID sets the organization ID in context. An authenticating interceptor
// that runs before the tenant-scope interceptor calls it with the caller's
// verified organization.
func WithOrgID(ctx context.Context, orgID string) context.Context {
	return withOrgID(ctx, orgID)
}

func withOrgID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxOrgID, id)
}
