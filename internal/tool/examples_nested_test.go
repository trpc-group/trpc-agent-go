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

// Pet defines the user's fury friend.
type Pet struct {
	// Name of the animal.
	Name string `json:"name" jsonschema:"title=Name"`
	// Animal type, e.g., dog, cat, hamster.
	AnimalType AnimalType `json:"animal_type" jsonschema:"title=Animal Type"`
}

type AnimalType string

// Pets is a collection of Pet objects.
type Pets []*Pet

// NamedPets is a map of animal names to pets.
type NamedPets map[string]*Pet

type (
	// Plant represents the plants the user might have and serves as a test
	// of structs inside a `type` set.
	Plant struct {
		Variant string `json:"variant" jsonschema:"title=Variant"` // This comment will be used
		// Multicellular is true if the plant is multicellular
		Multicellular bool `json:"multicellular,omitempty" jsonschema:"title=Multicellular"` // This comment will be ignored
	}
)
type User struct {
	// Unique sequential identifier.
	ID int `json:"id" jsonschema:"required"`
	// This comment will be ignored
	Name    string         `json:"name" jsonschema:"required,minLength=1,maxLength=20,pattern=.*,description=this is a property,title=the name,example=joe,example=lucy,default=alex"`
	Friends []int          `json:"friends,omitempty" jsonschema_description:"list of IDs, omitted when empty"`
	Tags    map[string]any `json:"tags,omitempty"`

	// An array of pets the user cares for.
	Pets Pets `json:"pets"`

	// Set of animal names to pets
	NamedPets NamedPets `json:"named_pets"`

	// Set of plants that the user likes
	Plants []*Plant `json:"plants" jsonschema:"title=Plants"`
}

func Test_GenerateJSONSchema_User(t *testing.T) {
	s := itool.GenerateJSONSchema(reflect.TypeOf(&User{}))
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err.Error())
	}
	t.Log(string(data))
}

func Test_GenerateJSONSchema_TreeNode(t *testing.T) {
	s := itool.GenerateJSONSchema(reflect.TypeOf(&TreeNode{}))
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err.Error())
	}
	t.Log(string(data))
}

func Test_GenerateJSONSchema_LinkedListNode(t *testing.T) {
	s := itool.GenerateJSONSchema(reflect.TypeOf(&LinkedListNode{}))
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err.Error())
	}
	t.Log(string(data))
}

type TreeLinkListNode struct {
	Name         string          `json:"name"`
	TreeNode     *TreeNode       `json:"tree_node,omitempty"`
	LinkListNode *LinkedListNode `json:"link_list_node,omitempty"`
}

func Test_GenerateJSONSchema_TreeLinkListNode(t *testing.T) {
	s := itool.GenerateJSONSchema(reflect.TypeOf(&TreeLinkListNode{}))
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err.Error())
	}
	t.Log(string(data))
}
