//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExternalBackendsEmptyWithoutEnv(t *testing.T) {
	os.Unsetenv("REPLAYTEST_REDIS_ADDR")
	os.Unsetenv("REPLAYTEST_POSTGRES_DSN")
	require.Empty(t, externalBackends(testSummarizer{}))
}
