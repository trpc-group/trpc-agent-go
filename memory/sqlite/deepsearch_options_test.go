//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type optionModel struct{}

func (m optionModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m optionModel) Info() model.Info {
	return model.Info{Name: "option-test"}
}

func TestWithDeepSearchControlsSearchToolSchema(t *testing.T) {
	nilOpts := defaultOptions.clone()
	WithDeepSearch(nil)(&nilOpts)
	assertSearchModeSchema(t, nilOpts, false)

	enabledOpts := defaultOptions.clone()
	WithDeepSearch(optionModel{})(&enabledOpts)
	assertSearchModeSchema(t, enabledOpts, true)
}

func assertSearchModeSchema(t *testing.T, opts ServiceOpts, want bool) {
	t.Helper()
	searchTool := opts.toolCreators[memory.SearchToolName]()
	schema := searchTool.Declaration().InputSchema
	require.NotNil(t, schema)
	_, ok := schema.Properties["search_mode"]
	require.Equal(t, want, ok)
}
