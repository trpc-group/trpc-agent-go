//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func TestFetchRemoteStructure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/trpc-agent/v1/apps/promptiter-nba-commentary-candidate/structure", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(struct {
			Structure *astructure.Snapshot `json:"structure"`
		}{
			Structure: &astructure.Snapshot{
				StructureID: "structure-1",
				EntryNodeID: candidateAgentName,
				Nodes: []astructure.Node{{
					NodeID: candidateAgentName,
					Kind:   astructure.NodeKindLLM,
					Name:   candidateAgentName,
				}},
				Surfaces: []astructure.Surface{{
					SurfaceID: "candidate#instruction",
					NodeID:    candidateAgentName,
					Type:      astructure.SurfaceTypeInstruction,
					Value:     astructure.SurfaceValue{Text: stringPtr("base prompt")},
				}},
			},
		}))
	}))
	defer server.Close()
	snapshot, err := fetchRemoteStructure(context.Background(), candidateAppName, server.URL, "/trpc-agent/v1/apps")
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, "structure-1", snapshot.StructureID)
	require.Len(t, snapshot.Surfaces, 1)
	assert.Equal(t, "candidate#instruction", snapshot.Surfaces[0].SurfaceID)
}

func stringPtr(value string) *string {
	return &value
}
