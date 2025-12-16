//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_Close tests Close method
func TestVectorStore_Close(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "close_success",
			wantErr: false,
		},
		{
			name:    "close_multiple_times",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			vs := newTestVectorStore(t, mc)

			err := vs.Close()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Close again to test idempotency
			if tt.name == "close_multiple_times" {
				err = vs.Close()
				require.NoError(t, err)
			}
		})
	}
}

// TestVectorStore_GetMetadata tests GetMetadata method
func TestVectorStore_GetMetadata(t *testing.T) {
	tests := []struct {
		name      string
		setupDocs func(*mockClient)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, map[string]vectorstore.DocumentMetadata)
	}{
		{
			name: "get_all_metadata",
			setupDocs: func(mc *mockClient) {
				// Setup search hits for GetMetadata
				mc.SetSearchHits([]map[string]any{
					{
						"_source": map[string]any{
							"id":      "doc1",
							"name":    "Doc 1",
							"content": "Content 1",
						},
					},
					{
						"_source": map[string]any{
							"id":      "doc2",
							"name":    "Doc 2",
							"content": "Content 2",
						},
					},
				})
			},
			wantErr: false,
			validate: func(t *testing.T, metadata map[string]vectorstore.DocumentMetadata) {
				assert.NotNil(t, metadata)
			},
		},
		{
			name: "client_error",
			setupDocs: func(mc *mockClient) {
				mc.SetSearchError(errors.New("search failed"))
			},
			wantErr: true,
			errMsg:  "search failed",
		},
		{
			name: "doc_builder_error_is_tolerated",
			setupDocs: func(mc *mockClient) {
				mc.SetSearchHits([]map[string]any{
					{
						"_source": map[string]any{
							"id":      "doc1",
							"name":    "Doc 1",
							"content": "Content 1",
						},
					},
				})
			},
			wantErr: false,
			validate: func(
				t *testing.T,
				metadata map[string]vectorstore.DocumentMetadata,
			) {
				assert.NotNil(t, metadata)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			tt.setupDocs(mc)
			builder := func(json.RawMessage) (*document.Document, []float64, error) {
				return nil, nil, errors.New("doc builder failed")
			}
			opts := []Option{}
			if tt.name == "doc_builder_error_is_tolerated" {
				opts = append(opts, WithDocBuilder(builder))
			}
			vs := newTestVectorStore(t, mc, opts...)

			metadata, err := vs.GetMetadata(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, metadata)
				}
			}
		})
	}
}
