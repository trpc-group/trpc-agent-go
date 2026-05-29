//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package modelcontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestResolveContextWindowUsesInstanceBeforeRegistry(t *testing.T) {
	const modelName = "resolve-instance-window"
	model.RegisterModelContextWindow(modelName, 12345)

	window, ok := ResolveContextWindow(contextWindowTestModel{
		name:   modelName,
		window: 54321,
	})
	assert.True(t, ok)
	assert.Equal(t, 54321, window)
}

func TestResolveContextWindowFallsBackToRegistry(t *testing.T) {
	window, ok := ResolveContextWindow(contextWindowTestModel{
		name: "gpt-4o-mini",
	})
	assert.True(t, ok)
	assert.Equal(t, 128000, window)
}

type contextWindowTestModel struct {
	name   string
	window int
}

func (m contextWindowTestModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m contextWindowTestModel) Info() model.Info {
	return model.Info{
		Name:          m.name,
		ContextWindow: m.window,
	}
}
