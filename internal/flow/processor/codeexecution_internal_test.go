//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestCodeExecutorForInvocation_NilInvocation(t *testing.T) {
	require.Nil(t, codeExecutorForInvocation(nil))
}

func TestCodeExecutorForInvocation_NoAgentExecutor(t *testing.T) {
	require.Nil(t, codeExecutorForInvocation(&agent.Invocation{}))
}
