//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import "testing"

// requirePinnedWalk skips tests that need a successful no-follow
// directory traversal. Platforms without openat fail-close staging
// instead of reopening pathnames.
func requirePinnedWalk(t *testing.T) {
	t.Helper()
	if !pinnedWalkSupported {
		t.Skip("directory staging fail-closes without openat on this platform")
	}
}
