//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"os"
	"testing"
)

// TestMain neutralizes the ambient process environment for the whole package.
// The network rules inspect the environment the executed command inherits (see
// effectiveProxyEnv), so a developer or CI machine that exports HTTPS_PROXY
// would otherwise change the outcome of every network test. Tests that care
// about the inherited environment install their own osEnviron.
func TestMain(m *testing.M) {
	osEnviron = func() []string { return nil }
	os.Exit(m.Run())
}
