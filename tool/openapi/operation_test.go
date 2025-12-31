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

	openapi "github.com/getkin/kin-openapi/openapi3"
)

func Test_methodOperations(t *testing.T) {
	loader, _ := NewFileLoader("../../examples/openapitool/petstore3.yaml")
	doc, _ := loader.Load(context.Background())
	l := doc.Paths.Len()
	if l == 0 {
		return
	}
	var item *openapi.PathItem
	for _, pi := range doc.Paths.Map() {
		if pi != nil {
			item = pi
			break
		}
	}
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		p *openapi.PathItem
	}{
		{
			name: "methodOperations_OK",
			p:    item,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for method := range methodOperations(tt.p) {
				t.Logf("range methodOperations() = %v", method)
			}
		})
	}
}

func TestOperationName(t *testing.T) {
	tests := []struct {
		name     string
		op       *openapi.Operation
		path     string
		method   string
		expected string
	}{
		{
			name: "operation_with_empty_operation_id",
			op: &openapi.Operation{
				OperationID: "",
			},
			path:     "/users",
			method:   "POST",
			expected: "post_/users",
		},
		{
			name: "operation_with_valid_operation_id",
			op: &openapi.Operation{
				OperationID: "createUser",
			},
			path:     "/users",
			method:   "POST",
			expected: "createUser",
		},
		{
			name: "operation_with_operation_id_and_empty_path_method",
			op: &openapi.Operation{
				OperationID: "testOperation",
			},
			path:     "",
			method:   "",
			expected: "testOperation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := operationName(tt.op, tt.path, tt.method)
			if result != tt.expected {
				t.Errorf("operationName() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestOperationDesc(t *testing.T) {
	tests := []struct {
		name     string
		op       *openapi.Operation
		expected string
	}{
		{
			name: "operation_with_description_and_summary",
			op: &openapi.Operation{
				Description: "This is a description",
				Summary:     "This is a summary",
			},
			expected: "This is a description",
		},
		{
			name: "operation_with_empty_description_but_has_summary",
			op: &openapi.Operation{
				Description: "",
				Summary:     "Operation summary",
			},
			expected: "Operation summary",
		},
		{
			name: "operation_with_empty_description_and_empty_summary",
			op: &openapi.Operation{
				Description: "",
				Summary:     "",
			},
			expected: "",
		},
		{
			name: "operation_with_description_only",
			op: &openapi.Operation{
				Description: "Only description provided",
				Summary:     "",
			},
			expected: "Only description provided",
		},
		{
			name: "operation_with_summary_only",
			op: &openapi.Operation{
				Description: "",
				Summary:     "Only summary provided",
			},
			expected: "Only summary provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := operationDesc(tt.op)
			if result != tt.expected {
				t.Errorf("operationDesc() = %q, want %q", result, tt.expected)
			}
		})
	}
}
