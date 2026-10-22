// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package grpctenant

import (
	"context"

	"google.golang.org/grpc"
)

// wrappedServerStream overrides Context() to return the tenant-scoped context.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context { return w.ctx }
