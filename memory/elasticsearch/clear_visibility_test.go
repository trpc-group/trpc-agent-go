//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// visibilityClient separates real-time documents from the searchable snapshot.
// DeleteByQuery sees the same snapshot as Search; only a refresh publishes writes
// and deletions. This models the visibility boundary missing from mockClient.
type visibilityClient struct {
	*mockClient
	searchable      map[string]map[string]any
	refreshCalls    int
	denyRefreshFrom int
}

func (m *visibilityClient) Search(ctx context.Context, indexName string, body []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	view := mockClient{docs: m.searchable}
	return view.evaluateSearch(body)
}

func (m *visibilityClient) DeleteByQuery(ctx context.Context, indexName string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var req mockSearchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	for id, doc := range m.searchable {
		if match, _ := evalQuery(req.Query, doc); match {
			delete(m.docs, id)
		}
	}
	return nil
}

func (m *visibilityClient) Refresh(ctx context.Context, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshCalls++
	if m.denyRefreshFrom > 0 && m.refreshCalls >= m.denyRefreshFrom {
		return m.refreshErr
	}
	m.publishSearchableLocked()
	return nil
}

// publishSearchable simulates a server-side periodic refresh independently of
// the caller's permission to invoke the refresh API. No sleeps are required.
func (m *visibilityClient) publishSearchable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishSearchableLocked()
}

func (m *visibilityClient) publishSearchableLocked() {
	m.searchable = make(map[string]map[string]any, len(m.docs))
	for id, doc := range m.docs {
		m.searchable[id] = maps.Clone(doc)
	}
}

func TestClearMemoriesDelayedVisibility(t *testing.T) {
	for _, tc := range []struct {
		name             string
		denyRefreshFrom  int
		wantImmediate    int
		wantAfterRefresh int
		wantError        string
	}{
		{name: "refresh allowed"},
		{
			name: "pre-clear refresh rejected", denyRefreshFrom: 1,
			wantImmediate: 1, wantAfterRefresh: 2, wantError: "pre-clear refresh failed",
		},
		{
			name: "post-clear refresh rejected", denyRefreshFrom: 2,
			wantImmediate: 2, wantError: "post-clear refresh failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &visibilityClient{
				mockClient:      newMockClient(),
				denyRefreshFrom: tc.denyRefreshFrom,
			}
			client.refreshErr = errors.New("elasticsearch refresh index failed: 403: missing maintenance privilege")
			svc, err := NewService(
				WithIndexDimension(testDimension),
				WithEmbedder(newStubEmbedder(testDimension)),
				withClient(client),
			)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, svc.Close()) })

			ctx := context.Background()
			user := testUserKey("app", "user")
			other := testUserKey("app", "other-user")
			require.NoError(t, svc.AddMemory(ctx, user, "already searchable", nil))
			require.NoError(t, svc.AddMemory(ctx, other, "keep other scope", nil))
			client.publishSearchable()
			require.NoError(t, svc.AddMemory(ctx, user, "not searchable yet", nil))
			before, err := svc.ReadMemories(ctx, user, 0)
			require.NoError(t, err)
			require.Len(t, before, 1, "the new write must remain outside the search snapshot")

			clearErr := svc.ClearMemories(ctx, user)
			immediate, err := svc.ReadMemories(ctx, user, 0)
			require.NoError(t, err)
			client.publishSearchable()
			afterRefresh, err := svc.ReadMemories(ctx, user, 0)
			require.NoError(t, err)
			t.Logf("clear error=%v; memories immediately=%d; after periodic refresh=%d",
				clearErr, len(immediate), len(afterRefresh))

			if tc.denyRefreshFrom == 0 {
				require.NoError(t, clearErr)
			} else {
				require.ErrorIs(t, clearErr, client.refreshErr)
				assert.ErrorContains(t, clearErr, tc.wantError)
			}
			assert.Len(t, immediate, tc.wantImmediate)
			assert.Len(t, afterRefresh, tc.wantAfterRefresh)
			remaining, err := svc.ReadMemories(ctx, other, 0)
			require.NoError(t, err)
			assert.Len(t, remaining, 1, "clear must preserve other users' memories")
		})
	}
}
