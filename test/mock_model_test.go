//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestQueueModelConsumesCallsInOrder(t *testing.T) {
	m := &QueueModel{}
	m.Push(responseCall("first"))
	m.Push(responseCall("second"))

	ids, err := generateResponseIDs(m)
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, ids)

	ids, err = generateResponseIDs(m)
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, ids)

	_, err = m.GenerateContent(context.Background(), &model.Request{})
	require.ErrorContains(t, err, "no queued calls")
}

func TestQueueModelNilRequestDoesNotConsumeCall(t *testing.T) {
	m := &QueueModel{}
	m.Push(responseCall("first"))

	_, err := m.GenerateContent(context.Background(), nil)
	require.ErrorContains(t, err, "request is nil")

	ids, err := generateResponseIDs(m)
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, ids)
}

func TestQueueModelConcurrentCallsConsumeEachCallOnce(t *testing.T) {
	const callCount = 8

	m := &QueueModel{}
	for i := 0; i < callCount; i++ {
		m.Push(responseCall(fmt.Sprintf("response-%d", i)))
	}

	type result struct {
		ids []string
		err error
	}
	results := make(chan result, callCount)
	var wg sync.WaitGroup
	for i := 0; i < callCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, err := generateResponseIDs(m)
			results <- result{ids: ids, err: err}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]int, callCount)
	for result := range results {
		require.NoError(t, result.err)
		require.Len(t, result.ids, 1)
		seen[result.ids[0]]++
	}
	for i := 0; i < callCount; i++ {
		require.Equal(t, 1, seen[fmt.Sprintf("response-%d", i)])
	}
}

func responseCall(id string) Call {
	return Call{Responses: []*model.Response{{ID: id}}}
}

func generateResponseIDs(m *QueueModel) ([]string, error) {
	responses, err := m.GenerateContent(context.Background(), &model.Request{})
	if err != nil {
		return nil, err
	}

	var ids []string
	for response := range responses {
		ids = append(ids, response.ID)
	}
	return ids, nil
}
