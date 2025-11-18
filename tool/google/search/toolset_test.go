//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package search

import (
	"context"
	"reflect"
	"testing"
)

func TestOption(t *testing.T) {
	type testCase struct {
		name    string
		opts    []Option
		wantCfg config
	}

	testCases := []testCase{
		{
			name: "empty",
			opts: nil,
			wantCfg: config{
				size:   0,
				offset: 0,
				lang:   "",
			},
		},
		{
			name: "with options",
			opts: []Option{
				WithSize(10),
				WithLanguage("fr"),
				WithOffset(10),
			},
			wantCfg: config{
				size:   10,
				offset: 10,
				lang:   "fr",
			},
		},
		{
			name: "custom option",
			opts: []Option{
				WithEngineID("123456789"),
			},
			wantCfg: config{
				engineID: "123456789",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config{}
			for _, opt := range tc.opts {
				opt(cfg)
			}
			if !reflect.DeepEqual(*cfg, tc.wantCfg) {
				t.Errorf("got config %v, want %v", *cfg, tc.wantCfg)
			}
		})
	}
}

func TestNewToolSet(t *testing.T) {
	srv := newFakeGoogleSearch(false)
	defer srv.Close()
	type testCase struct {
		name      string
		opts      []Option
		wantErr   bool
		wantTools int
	}
	testCases := []testCase{
		{
			name: "with options",
			opts: []Option{
				WithAPIKey("123456789"),
				WithEngineID("123456789"),
				WithBaseURL(srv.URL),
			},
			wantErr:   false,
			wantTools: 1,
		},
		{
			name: "custom option",
			opts: []Option{
				WithAPIKey("123456789"),
				WithEngineID("123456789"),
				WithBaseURL(srv.URL),
			},
			wantErr:   false,
			wantTools: 1,
		},
		{
			name:      "empty api key with search tool",
			wantErr:   true,
			wantTools: 0,
		},
		{
			name: "empty engine id with search tool",
			opts: []Option{
				WithAPIKey("123456789"),
			},
			wantErr:   true,
			wantTools: 0,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toolSet, err := NewToolSet(context.Background(), tc.opts...)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(toolSet.tools) != tc.wantTools {
				t.Errorf("got %d tools, want %d", len(toolSet.tools), tc.wantTools)
			}
		})
	}
}

func TestTools(t *testing.T) {
	srv := newFakeGoogleSearch(false)
	defer srv.Close()
	toolSet, err := NewToolSet(context.Background(),
		WithAPIKey("123456789"),
		WithEngineID("123456789"),
		WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	tools := toolSet.Tools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
}

func TestName(t *testing.T) {
	srv := newFakeGoogleSearch(false)
	defer srv.Close()
	toolSet, err := NewToolSet(context.Background(),
		WithAPIKey("123456789"),
		WithEngineID("123456789"),
		WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	if toolSet.Name() != "google" {
		t.Fatalf("got name %s, want google", toolSet.Name())
	}
}

func TestClose(t *testing.T) {
	srv := newFakeGoogleSearch(false)
	defer srv.Close()
	toolSet, err := NewToolSet(context.Background(), WithAPIKey("123456789"), WithEngineID("123456789"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	if err := toolSet.Close(); err != nil {
		t.Fatalf("failed to close tool set: %v", err)
	}
}
