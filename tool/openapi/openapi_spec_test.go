//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openapi

import (
	"context"
	"testing"
)

func Test_urlSpecLoader_Load(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for receiver constructor.
		uri     string
		wantErr bool
	}{
		{
			name:    "LoadFromURI_INVALID_URL",
			uri:     "http://localhost:8080",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _ := NewURILoader(tt.uri, WithExternalRefs(true))
			_, gotErr := u.Load(context.Background())
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Load() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Load() succeeded unexpectedly")
			}
		})
	}
}
