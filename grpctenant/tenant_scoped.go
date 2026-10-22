// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package grpctenant

// TenantScoped is implemented by proto messages that carry an organization_id field.
// Generated protobuf code already provides this getter for messages with organization_id.
type TenantScoped interface {
	GetOrganizationId() string
}
