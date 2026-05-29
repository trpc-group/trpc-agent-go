//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package age

import (
	"strings"
	"testing"
)

func applyOptions(opts ...Option) options {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func TestOptionFunctions(t *testing.T) {
	tests := []struct {
		name      string
		opt       Option
		checkFunc func(t *testing.T, o options)
	}{
		{
			name: "WithGraphName sets graphName",
			opt:  WithGraphName("my_graph"),
			checkFunc: func(t *testing.T, o options) {
				if o.graphName != "my_graph" {
					t.Fatalf("graphName = %q, want %q", o.graphName, "my_graph")
				}
			},
		},
		{
			name: "WithClientDSN sets dsn",
			opt:  WithClientDSN("postgres://localhost/test"),
			checkFunc: func(t *testing.T, o options) {
				if o.dsn != "postgres://localhost/test" {
					t.Fatalf("dsn = %q, want %q", o.dsn, "postgres://localhost/test")
				}
			},
		},
		{
			name: "WithPostgresInstance sets instanceName",
			opt:  WithPostgresInstance("myinst"),
			checkFunc: func(t *testing.T, o options) {
				if o.instanceName != "myinst" {
					t.Fatalf("instanceName = %q, want %q", o.instanceName, "myinst")
				}
			},
		},
		{
			name: "default graphName is preserved without WithGraphName",
			opt:  WithClientDSN("dsn"),
			checkFunc: func(t *testing.T, o options) {
				if o.graphName != defaultGraphName {
					t.Fatalf("graphName = %q, want default %q", o.graphName, defaultGraphName)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := applyOptions(tc.opt)
			tc.checkFunc(t, o)
		})
	}
}

func TestBuilderOptions(t *testing.T) {
	tests := []struct {
		name       string
		opts       options
		wantLen    int
		wantNil    bool
		wantErrSub string
	}{
		{
			name:       "neither DSN nor instance returns error",
			opts:       defaultOptions,
			wantErrSub: "requires WithClientDSN or WithPostgresInstance",
		},
		{
			name:    "DSN set returns one builder opt",
			opts:    applyOptions(WithClientDSN("postgres://localhost/test")),
			wantLen: 1,
		},
		{
			name:       "unregistered instance returns error",
			opts:       applyOptions(WithPostgresInstance("nonexistent")),
			wantErrSub: "not found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.opts.builderOptions()
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len(builderOptions) = %d, want %d", len(got), tc.wantLen)
			}
		})
	}
}
