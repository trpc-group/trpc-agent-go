//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replacementtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/replacementtest"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestContractAgainstReferenceService(t *testing.T) {
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	replacementtest.Run(t, service)
	replacementtest.RunAsync(t, service)
}
