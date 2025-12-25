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
	loader := NewFileLoader("../../examples/openapitool/petstore3.yaml")
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
