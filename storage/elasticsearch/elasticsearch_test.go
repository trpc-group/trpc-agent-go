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
	"fmt"
	"testing"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
	"github.com/stretchr/testify/require"
)

func TestDefaultClientBuilder_VersionSelection(t *testing.T) {
	// Default (unspecified) -> v9
	c, err := defaultClientBuilder(
		WithVersion(ESVersionUnspecified),
	)
	require.NoError(t, err)
	_, ok := c.(*esv9.Client)
	require.True(t, ok)

	// Explicit v9
	c, err = defaultClientBuilder(WithVersion(ESVersionV9))
	require.NoError(t, err)
	_, ok = c.(*esv9.Client)
	require.True(t, ok)

	// v8
	c, err = defaultClientBuilder(WithVersion(ESVersionV8))
	require.NoError(t, err)
	_, ok = c.(*esv8.Client)
	require.True(t, ok)

	// v7
	c, err = defaultClientBuilder(WithVersion(ESVersionV7))
	require.NoError(t, err)
	_, ok = c.(*esv7.Client)
	require.True(t, ok)

	// unknown
	_, err = defaultClientBuilder(WithVersion(ESVersion("unknown")))
	require.Error(t, err)
	require.Equal(t, "elasticsearch: unknown version unknown", err.Error())
}

func TestWrapSDKClient(t *testing.T) {
	tests := []struct {
		name     string
		client   any
		wantErr  bool
		errMsg   string
		wantType string
	}{
		{
			name:     "v7 client",
			client:   &esv7.Client{},
			wantErr:  false,
			wantType: "*elasticsearch.clientV7",
		},
		{
			name:     "v8 client",
			client:   &esv8.Client{},
			wantErr:  false,
			wantType: "*elasticsearch.clientV8",
		},
		{
			name:     "v9 client",
			client:   &esv9.Client{},
			wantErr:  false,
			wantType: "*elasticsearch.clientV9",
		},
		{
			name:    "nil client",
			client:  nil,
			wantErr: true,
			errMsg:  "elasticsearch client is not supported, type: <nil>",
		},
		{
			name:    "unsupported string type",
			client:  "invalid",
			wantErr: true,
			errMsg:  "elasticsearch client is not supported, type: string",
		},
		{
			name:    "unsupported integer type",
			client:  123,
			wantErr: true,
			errMsg:  "elasticsearch client is not supported, type: int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WrapSDKClient(tt.client)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					require.Equal(t, tt.errMsg, err.Error())
				}
				require.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			gotType := getTypeName(got)
			require.Equal(t, tt.wantType, gotType)
		})
	}
}

// getTypeName returns the type name of the given client implementation.
func getTypeName(client any) string {
	return fmt.Sprintf("%T", client)
}
