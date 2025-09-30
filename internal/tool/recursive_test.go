//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool_test

import (
	"encoding/json"
	"reflect"
	"testing"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
)

// TreeNode represents a recursive tree structure
type TreeNode struct {
	Name     string      `json:"name"`
	Children []*TreeNode `json:"children,omitempty"`
}

// LinkedListNode represents a recursive linked list structure
type LinkedListNode struct {
	Value int             `json:"value"`
	Next  *LinkedListNode `json:"next,omitempty"`
}

// MutuallyRecursiveA and MutuallyRecursiveB represent mutually recursive structures
type MutuallyRecursiveA struct {
	Name string              `json:"name"`
	B    *MutuallyRecursiveB `json:"b,omitempty"`
}

type MutuallyRecursiveB struct {
	Value int                 `json:"value"`
	A     *MutuallyRecursiveA `json:"a,omitempty"`
}

func TestGenerateJSONSchema_RecursiveStructure(t *testing.T) {
	t.Run("tree node recursive structure", func(t *testing.T) {
		// This should not panic and should generate a schema with $ref and $defs
		result := itool.GenerateJSONSchema(reflect.TypeOf(TreeNode{}))
		resultJson, _ := json.MarshalIndent(result, "", "  ")
		t.Logf("%s", resultJson)

		if result.Type != "object" {
			t.Errorf("expected object type, got %s", result.Type)
		}

		// Check that we have properties
		if result.Properties == nil {
			t.Fatal("expected properties to be set")
		}

		// Check name property
		if result.Properties["name"] == nil || result.Properties["name"].Type != "string" {
			t.Errorf("expected name property of type string")
		}

		// Check children property
		if result.Properties["children"] == nil || result.Properties["children"].Type != "array" {
			t.Errorf("expected children property of type array")
		}

		// The children items should use $ref to avoid infinite recursion
		if result.Properties["children"].Items == nil {
			t.Fatal("expected children items to be defined")
		}

		// Check if we have $defs defined for recursive types
		if result.Defs == nil {
			t.Errorf("expected $defs to be defined for recursive structure")
		}

		// Check that children items use $ref
		if result.Properties["children"].Items.Ref != "#/$defs/treenode" {
			t.Errorf("expected children items to reference #/$defs/treenode, got %s", result.Properties["children"].Items.Ref)
		}

		// Check the definition in $defs
		treeDef := result.Defs["treenode"]
		if treeDef == nil {
			t.Fatal("expected treenode definition in $defs")
		}

		if treeDef.Type != "object" {
			t.Errorf("expected treenode definition type to be object, got %s", treeDef.Type)
		}

		// Check that the definition also uses $ref for children
		if treeDef.Properties["children"].Items == nil || treeDef.Properties["children"].Items.Ref != "#/$defs/treenode" {
			t.Errorf("expected treenode definition children items to reference #/$defs/treenode")
		}
	})

	t.Run("linked list recursive structure", func(t *testing.T) {
		// This should not panic and should generate a schema with $ref and $defs
		result := itool.GenerateJSONSchema(reflect.TypeOf(LinkedListNode{}))
		resultJson, _ := json.MarshalIndent(result, "", "  ")
		t.Logf("%s", resultJson)

		if result.Type != "object" {
			t.Errorf("expected object type, got %s", result.Type)
		}

		// Check that we have properties
		if result.Properties == nil {
			t.Fatal("expected properties to be set")
		}

		// Check value property
		if result.Properties["value"] == nil || result.Properties["value"].Type != "integer" {
			t.Errorf("expected value property of type integer")
		}

		// Check next property - should use $ref to avoid infinite recursion
		if result.Properties["next"] == nil {
			t.Fatal("expected next property to be defined")
		}

		// Check if we have $defs defined for recursive types
		if result.Defs == nil {
			t.Errorf("expected $defs to be defined for recursive structure")
		}
	})

	t.Run("mutually recursive structures", func(t *testing.T) {
		// This should not panic and should generate a schema with $ref and $defs
		result := itool.GenerateJSONSchema(reflect.TypeOf(MutuallyRecursiveA{}))
		resultJson, _ := json.MarshalIndent(result, "", "  ")
		t.Logf("%s", resultJson)

		if result.Type != "object" {
			t.Errorf("expected object type, got %s", result.Type)
		}

		// Check that we have $defs for both types
		if result.Defs == nil {
			t.Fatal("expected $defs to be defined for mutually recursive structures")
		}

		// Should have definitions for both types
		expectedDefs := 2 // MutuallyRecursiveA and MutuallyRecursiveB
		if len(result.Defs) < expectedDefs {
			t.Errorf("expected at least %d definitions in $defs, got %d", expectedDefs, len(result.Defs))
		}
	})
}
