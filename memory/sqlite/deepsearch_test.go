//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type backendDeepSearchModel struct{}

func (*backendDeepSearchModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Choices: []model.Choice{{Message: model.Message{
		Content: `{"memories":[{"id":"m1","cues":["graduation degree"],"tags":["education"]}]}`,
	}}}}
	close(responses)
	return responses, nil
}

func (*backendDeepSearchModel) Info() model.Info { return model.Info{Name: "deepsearch-test"} }

func TestServiceDeepSearchAdapter(t *testing.T) {
	indexModel := new(backendDeepSearchModel)
	opts := defaultOptions.clone()
	WithDeepSearch(indexModel, deepsearch.WithLimits(2, 3))(&opts)
	require.Same(t, indexModel, opts.deepSearchModel)
	require.Len(t, opts.deepSearchOptions, 1)
	require.Equal(t, defaultOptions.enabledTools, opts.enabledTools)

	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	entry := backendDeepSearchEntry(userKey)
	runtime := deepsearch.NewRuntime(indexModel, func(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
		return []*memory.Entry{entry}, nil
	}, opts.deepSearchOptions...)
	service := &Service{serviceDeepSearch: &serviceDeepSearch{Runtime: runtime}}
	var queryService deepsearch.QueryService = service
	require.NoError(t, queryService.EnsureIndex(context.Background(), userKey))
	result, err := queryService.SearchCues(context.Background(), deepsearch.CueSearchRequest{
		UserKey: userKey, Query: "graduation degree",
	})
	require.NoError(t, err)
	require.Len(t, result.Cues, 1)
}

func backendDeepSearchEntry(userKey memory.UserKey) *memory.Entry {
	now := time.Now()
	return &memory.Entry{
		ID: "memory-1", AppName: userKey.AppName, UserID: userKey.UserID,
		CreatedAt: now, UpdatedAt: now,
		Memory: &memory.Memory{Memory: "User graduated with a business degree.", Topics: []string{"education"}},
	}
}
