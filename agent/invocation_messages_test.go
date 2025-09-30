//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWithMessagesOption_SetsRunOptions(t *testing.T) {
	msgs := []model.Message{
		model.NewSystemMessage("s"),
		model.NewUserMessage("hi"),
	}
	var ro RunOptions
	WithMessages(msgs)(&ro)

	require.Equal(t, 2, len(ro.Messages))
	require.Equal(t, msgs[0].Role, ro.Messages[0].Role)
	require.Equal(t, msgs[1].Content, ro.Messages[1].Content)
}
