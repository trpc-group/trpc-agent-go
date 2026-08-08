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

	"github.com/stretchr/testify/require"
)

func newTestScanner(t testing.TB, policy Policy, opts ...scannerOption) *scanner {
	t.Helper()
	scanner, err := newScanner(policy, opts...)
	require.NoError(t, err)
	return scanner
}

// saveFile writes data to path with 0600 permissions. Used only by tests
// to avoid importing os in every test file.
func saveFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
