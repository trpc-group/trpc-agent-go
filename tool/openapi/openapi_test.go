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
	"bytes"
	"testing"
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
					WithSpecLoader(NewDataLoader(bytes.NewReader([]byte(invalidSpecNoServerProvided)))),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid_spec_cannot_resolve_reference",
			args: args{
				opts: []Option{
					WithSpecLoader(NewDataLoader(bytes.NewReader([]byte(invalidSpec)))),
				},
			},
			wantErr: true,
		},
		{
			name: "create_toolset_ok_yaml",
			args: args{
				opts: []Option{
					WithSpecLoader(NewFileLoader("../../examples/openapitool/petstore3.yaml")),
				},
			},
			wantErr: false,
		},
		{
			name: "create_toolset_ok_json",
			args: args{
				opts: []Option{
					WithSpecLoader(NewFileLoader("../../examples/openapitool/petstore3.json")),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewToolSet(tt.args.opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewToolSet() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}
