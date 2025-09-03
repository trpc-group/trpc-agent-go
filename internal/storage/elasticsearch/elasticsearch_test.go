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
	"net/http"
	"testing"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTripper allows mocking http.Transport for testing purposes.
type roundTripper func(*http.Request) *http.Response

// RoundTrip implements the http.RoundTripper interface.
func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

// TestNewClient tests the NewClient function with various input types.
func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		client      any
		wantType    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "elasticsearch v7 client",
			client:   &esv7.Client{},
			wantType: "*elasticsearch.clientV7",
			wantErr:  false,
		},
		{
			name:     "elasticsearch v8 client",
			client:   &esv8.Client{},
			wantType: "*elasticsearch.clientV8",
			wantErr:  false,
		},
		{
			name:     "elasticsearch v9 client",
			client:   &esv9.Client{},
			wantType: "*elasticsearch.clientV9",
			wantErr:  false,
		},
		{
			name:        "nil client input",
			client:      nil,
			wantErr:     true,
			errContains: "elasticsearch client is not supported, type: <nil>",
		},
		{
			name:        "unsupported string type",
			client:      "invalid",
			wantErr:     true,
			errContains: "elasticsearch client is not supported, type: string",
		},
		{
			name:        "unsupported integer type",
			client:      123,
			wantErr:     true,
			errContains: "elasticsearch client is not supported, type: int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewClient(tt.client)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Equal(t, tt.errContains, err.Error())
				}
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			gotType := getTypeName(got)
			assert.Equal(t, tt.wantType, gotType)
		})
	}
}

// getTypeName returns the type name of the given client implementation.
// This helper function is used for testing to verify the correct client type is returned.
func getTypeName(v any) string {
	switch v.(type) {
	case *clientV7:
		return "*elasticsearch.clientV7"
	case *clientV8:
		return "*elasticsearch.clientV8"
	case *clientV9:
		return "*elasticsearch.clientV9"
	default:
		return "unknown"
	}
}
