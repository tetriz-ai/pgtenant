// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

package testpg

// CheckWithin is Check with a shorter wait, so a test of an unreachable server
// does not wait the full 30 seconds.
var CheckWithin = check
