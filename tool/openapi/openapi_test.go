//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package openapi provides a toolset for a given openapi API specification.
package openapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	openapi "github.com/getkin/kin-openapi/openapi3"
)

const (
	invalidSpecNoServerProvided = `
openapi: 3.1.0
info:
  title: test-openapi-tool
servers:
  - url: http://localhost/api/v31
`
	invalidSpec = `
openapi: 3.0.4
info:
  title: test-openapi-tool
servers:
  - url: http://localhost/api/v3
paths:
  /pet:
    put:
      summary: Update an existing pet.
      description: Update an existing pet by Id.
      operationId: updatePet
      requestBody:
        description: Update an existent pet in the store
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Pet'
        required: true
      responses:
        "200":
          description: Successful operation
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Pet'
            application/xml:
              schema:
                $ref: '#/components/schemas/Pet'
        "400":
          description: Invalid ID supplied
        "404":
          description: Pet not found
        "422":
          description: Validation exception
        default:
          description: Unexpected error
`
)

func TestNewToolSet(t *testing.T) {
	invalidSpecNoServerProvidedLoader, _ := NewDataLoader([]byte(invalidSpecNoServerProvided))
	invalidSpecLoader, _ := NewDataLoader([]byte(invalidSpec))
	petStoreSpecLoader, _ := NewFileLoader("../../examples/openapitool/petstore3.yaml")
	petStoreSpecLoaderJSON, _ := NewFileLoader("../../examples/openapitool/petstore3.json")
	type args struct {
		opts []Option
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "invalid_spec_no_server_provided",
			args: args{
				opts: []Option{
					WithSpecLoader(invalidSpecNoServerProvidedLoader),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid_spec_cannot_resolve_reference",
			args: args{
				opts: []Option{
					WithSpecLoader(invalidSpecLoader),
				},
			},
			wantErr: true,
		},
		{
			name: "create_toolset_ok_yaml",
			args: args{
				opts: []Option{
					WithSpecLoader(petStoreSpecLoader),
				},
			},
			wantErr: false,
		},
		{
			name: "create_toolset_ok_json",
			args: args{
				opts: []Option{
					WithSpecLoader(petStoreSpecLoaderJSON),
				},
			},
			wantErr: false,
		},
		{
			name: "create_toolset_with_opts",
			args: args{
				opts: []Option{
					WithSpecLoader(petStoreSpecLoaderJSON),
					WithUserAgent("trpc-agent-go-test"),
					WithName("trpc-agent-go-test"),
					WithHTTPClient(http.DefaultClient),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewToolSet(context.Background(), tt.args.opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewToolSet() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestOpenAPIToolSet_NameWithDefault(t *testing.T) {
	loader, _ := NewFileLoader("../../examples/openapitool/petstore3.json")
	// Test with default name
	toolSet, err := NewToolSet(context.Background(),
		WithSpecLoader(loader),
	)
	if err != nil {
		t.Fatalf("Failed to create toolset: %v", err)
	}

	name := toolSet.Name()
	if name != "openapi" {
		t.Errorf("Name() = %v, want %v", name, "openapi")
	}
}

func TestOpenAPIToolSet_ToolsContext(t *testing.T) {
	loader, _ := NewFileLoader("../../examples/openapitool/petstore3.json")
	toolSet, err := NewToolSet(context.Background(),
		WithSpecLoader(loader),
	)
	if err != nil {
		t.Fatalf("Failed to create toolset: %v", err)
	}

	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"Background context", context.Background()},
		{"WithCancel context", func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			return ctx
		}()},
		{"WithTimeout context", func() context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return ctx
		}()},
		{"WithValue context", context.WithValue(context.Background(), "test", "value")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := toolSet.Tools(tt.ctx)
			if tools == nil {
				t.Error("Tools() returned nil")
			}
			if len(tools) == 0 {
				t.Error("Tools() returned empty slice")
			}
		})
	}
}

func TestOpenAPITool_Declaration(t *testing.T) {
	loader, _ := NewFileLoader("../../examples/openapitool/petstore3.json")
	toolSet, err := NewToolSet(context.Background(),
		WithSpecLoader(loader),
	)
	if err != nil {
		t.Fatalf("Failed to create toolset: %v", err)
	}

	// Get tools from the toolset
	tools := toolSet.Tools(context.Background())
	if len(tools) == 0 {
		t.Fatal("No tools found in toolset")
	}

	// Test declaration for each tool
	for i, tool := range tools {
		t.Run(fmt.Sprintf("Tool_%d", i), func(t *testing.T) {
			// Cast to openAPITool to access Declaration method
			openAPITool, ok := tool.(*openAPITool)
			if !ok {
				t.Skip("Tool is not an openAPITool, skipping declaration test")
				return
			}

			declaration := openAPITool.Declaration()
			if declaration == nil {
				t.Error("Declaration() returned nil")
				return
			}

			// Test basic declaration fields
			if declaration.Name == "" {
				t.Error("Declaration name is empty")
			}

			if declaration.Description == "" {
				t.Error("Declaration description is empty")
			}

			if declaration.InputSchema == nil {
				t.Error("Declaration input schema is nil")
			}

			if declaration.OutputSchema == nil {
				t.Error("Declaration output schema is nil")
			}
		})
	}
}

func TestDocProcessor_InfoTitle(t *testing.T) {
	tests := []struct {
		name     string
		doc      *openapi.T
		expected string
	}{
		{
			name:     "nil_info_returns_empty_string",
			doc:      &openapi.T{Info: nil},
			expected: "",
		},
		{
			name: "valid_info_returns_title",
			doc: &openapi.T{
				Info: &openapi.Info{
					Title: "Test API",
				},
			},
			expected: "Test API",
		},
		{
			name: "empty_title_returns_empty_string",
			doc: &openapi.T{
				Info: &openapi.Info{
					Title: "",
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := newDocProcessor(tt.doc)
			result := processor.InfoTitle()
			if result != tt.expected {
				t.Errorf("InfoTitle() = %q, want %q", result, tt.expected)
			}
		})
	}
}
