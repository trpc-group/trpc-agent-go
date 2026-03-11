//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"testing"

	publicagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestResolveInvokeAgentStream(t *testing.T) {
	tests := []struct {
		name       string
		invocation *publicagent.Invocation
		genCfg     *model.GenerationConfig
		want       bool
	}{
		{
			name:       "invocation override true",
			invocation: &publicagent.Invocation{RunOptions: publicagent.RunOptions{Stream: boolPtr(true)}},
			genCfg:     &model.GenerationConfig{Stream: false},
			want:       true,
		},
		{
			name:       "invocation override false",
			invocation: &publicagent.Invocation{RunOptions: publicagent.RunOptions{Stream: boolPtr(false)}},
			genCfg:     &model.GenerationConfig{Stream: true},
			want:       false,
		},
		{
			name:   "generation config fallback",
			genCfg: &model.GenerationConfig{Stream: true},
			want:   true,
		},
		{
			name: "default false",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveInvokeAgentStream(tt.invocation, tt.genCfg); got != tt.want {
				t.Fatalf("ResolveInvokeAgentStream() = %v, want %v", got, tt.want)
			}
		})
	}
}
