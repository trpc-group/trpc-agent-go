//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

func Test_clientWrapper_Models(t *testing.T) {
	type fields struct {
		client *genai.Client
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "success",

			fields: fields{
				client: &genai.Client{
					Models: &genai.Models{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &clientWrapper{
				client: tt.fields.client,
			}
			assert.NotNil(t, c.Models())
		})
	}
}

func Test_modelsWrapper_GenerateContent(t *testing.T) {
	type fields struct {
		models *genai.Models
	}
	type args struct {
		ctx      context.Context
		model    string
		contents []*genai.Content
		config   *genai.GenerateContentConfig
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "success",
			fields: fields{
				models: &genai.Models{},
			},
			args: args{
				ctx:      context.Background(),
				model:    "model",
				contents: []*genai.Content{},
				config:   &genai.GenerateContentConfig{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("The code did not panic")
				}
			}()
			m := &modelsWrapper{
				models: tt.fields.models,
			}
			_, _ = m.GenerateContent(tt.args.ctx, tt.args.model, tt.args.contents, tt.args.config)
		})
	}
}

func Test_modelsWrapper_GenerateContentStream(t *testing.T) {
	type fields struct {
		models *genai.Models
	}
	type args struct {
		ctx      context.Context
		model    string
		contents []*genai.Content
		config   *genai.GenerateContentConfig
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "success",
			fields: fields{
				models: &genai.Models{},
			},
			args: args{
				ctx:      context.Background(),
				model:    "model",
				contents: []*genai.Content{},
				config:   &genai.GenerateContentConfig{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &modelsWrapper{
				models: tt.fields.models,
			}
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("The code did not panic")
				}
			}()
			m.GenerateContentStream(tt.args.ctx, tt.args.model, tt.args.contents, tt.args.config)
		})
	}
}
