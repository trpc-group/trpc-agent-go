//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestConvertMCPSchema_Basic(t *testing.T) {
	mcpSchema := map[string]any{
		"type":        "object",
		"description": "test schema",
		"required":    []any{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "number", "description": "bbb"},
		},
	}

	s := convertMCPSchemaToSchema(mcpSchema)
	require.Equal(t, "object", s.Type)
	require.Equal(t, "test schema", s.Description)
	require.ElementsMatch(t, []string{"a", "b"}, s.Required)
	require.Equal(t, "string", s.Properties["a"].Type)
	require.Equal(t, "number", s.Properties["b"].Type)
	require.Equal(t, "bbb", s.Properties["b"].Description)
}

func TestConvertProperties_Nil(t *testing.T) {
	require.Nil(t, convertProperties(nil))
}

func TestConvertMCPSchema_InvalidJSON(t *testing.T) {
	// Channel cannot marshal, expect fallback schema.
	schema := convertMCPSchemaToSchema(make(chan int))
	require.Equal(t, &tool.Schema{Type: "object"}, schema)
}

func TestConvertMCPSchema_NestedObjects(t *testing.T) {
	// Test nested object mapping issue
	mcpSchema := map[string]any{
		"type":        "object",
		"description": "schema with nested objects",
		"required":    []any{"user", "config"},
		"properties": map[string]any{
			"user": map[string]any{
				"type":        "object",
				"description": "user information",
				"required":    []any{"name", "email"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "user name",
					},
					"email": map[string]any{
						"type":        "string",
						"description": "user email",
					},
					"profile": map[string]any{
						"type":        "object",
						"description": "user profile",
						"properties": map[string]any{
							"age": map[string]any{
								"type":        "integer",
								"description": "user age",
							},
							"address": map[string]any{
								"type":        "object",
								"description": "user address",
								"properties": map[string]any{
									"city": map[string]any{
										"type":        "string",
										"description": "city name",
									},
									"country": map[string]any{
										"type":        "string",
										"description": "country name",
									},
								},
							},
						},
					},
				},
			},
			"config": map[string]any{
				"type":        "object",
				"description": "configuration settings",
				"properties": map[string]any{
					"theme": map[string]any{
						"type":        "string",
						"description": "UI theme",
						"enum":        []any{"light", "dark"},
					},
					"notifications": map[string]any{
						"type":        "object",
						"description": "notification settings",
						"properties": map[string]any{
							"email": map[string]any{
								"type":        "boolean",
								"description": "email notifications enabled",
								"default":     true,
							},
							"push": map[string]any{
								"type":        "boolean",
								"description": "push notifications enabled",
								"default":     false,
							},
						},
					},
				},
			},
		},
	}

	schema := convertMCPSchemaToSchema(mcpSchema)

	// Verify top-level properties
	require.Equal(t, "object", schema.Type)
	require.Equal(t, "schema with nested objects", schema.Description)
	require.ElementsMatch(t, []string{"user", "config"}, schema.Required)

	// Verify user object
	userSchema := schema.Properties["user"]
	require.NotNil(t, userSchema)
	require.Equal(t, "object", userSchema.Type)
	require.Equal(t, "user information", userSchema.Description)

	// Verify nested properties are correctly parsed
	require.NotNil(t, userSchema.Properties, "nested object properties should be parsed")
	require.Len(t, userSchema.Properties, 3, "user object should have 3 properties")

	if userSchema.Properties != nil {
		nameSchema := userSchema.Properties["name"]
		require.NotNil(t, nameSchema, "user.name property should be parsed")
		require.Equal(t, "string", nameSchema.Type)
		require.Equal(t, "user name", nameSchema.Description)

		emailSchema := userSchema.Properties["email"]
		require.NotNil(t, emailSchema, "user.email property should be parsed")
		require.Equal(t, "string", emailSchema.Type)
		require.Equal(t, "user email", emailSchema.Description)

		// Verify deeply nested profile object
		profileSchema := userSchema.Properties["profile"]
		require.NotNil(t, profileSchema, "user.profile property should be parsed")
		require.Equal(t, "object", profileSchema.Type)
		require.Equal(t, "user profile", profileSchema.Description)

		if profileSchema.Properties != nil {
			ageSchema := profileSchema.Properties["age"]
			require.NotNil(t, ageSchema, "user.profile.age property should be parsed")
			require.Equal(t, "integer", ageSchema.Type)

			// Verify even deeper nested address object
			addressSchema := profileSchema.Properties["address"]
			require.NotNil(t, addressSchema, "user.profile.address property should be parsed")
			require.Equal(t, "object", addressSchema.Type)

			if addressSchema.Properties != nil {
				citySchema := addressSchema.Properties["city"]
				require.NotNil(t, citySchema, "user.profile.address.city property should be parsed")
				require.Equal(t, "string", citySchema.Type)
				require.Equal(t, "city name", citySchema.Description)
			}
		}
	}

	// Verify config object
	configSchema := schema.Properties["config"]
	require.NotNil(t, configSchema)
	require.Equal(t, "object", configSchema.Type)
	require.Equal(t, "configuration settings", configSchema.Description)

	if configSchema.Properties != nil {
		themeSchema := configSchema.Properties["theme"]
		require.NotNil(t, themeSchema, "config.theme property should be parsed")
		require.Equal(t, "string", themeSchema.Type)
		require.Equal(t, "UI theme", themeSchema.Description)
		require.ElementsMatch(t, []any{"light", "dark"}, themeSchema.Enum)

		// Verify nested notifications object
		notificationsSchema := configSchema.Properties["notifications"]
		require.NotNil(t, notificationsSchema, "config.notifications property should be parsed")
		require.Equal(t, "object", notificationsSchema.Type)

		if notificationsSchema.Properties != nil {
			emailNotifSchema := notificationsSchema.Properties["email"]
			require.NotNil(t, emailNotifSchema, "config.notifications.email property should be parsed")
			require.Equal(t, "boolean", emailNotifSchema.Type)
			require.Equal(t, true, emailNotifSchema.Default)

			pushNotifSchema := notificationsSchema.Properties["push"]
			require.NotNil(t, pushNotifSchema, "config.notifications.push property should be parsed")
			require.Equal(t, "boolean", pushNotifSchema.Type)
			require.Equal(t, false, pushNotifSchema.Default)
		}
	}
}

func TestConvertMCPSchema_ArrayWithNestedObjects(t *testing.T) {
	// Test array with nested objects mapping
	mcpSchema := map[string]any{
		"type":        "object",
		"description": "schema with array of nested objects",
		"properties": map[string]any{
			"users": map[string]any{
				"type":        "array",
				"description": "list of users",
				"items": map[string]any{
					"type":        "object",
					"description": "user object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "integer",
							"description": "user id",
						},
						"name": map[string]any{
							"type":        "string",
							"description": "user name",
						},
					},
					"required": []any{"id", "name"},
				},
			},
		},
	}

	schema := convertMCPSchemaToSchema(mcpSchema)

	// Verify top-level schema
	require.Equal(t, "object", schema.Type)
	require.NotNil(t, schema.Properties)

	// Verify users array
	usersSchema := schema.Properties["users"]
	require.NotNil(t, usersSchema)
	require.Equal(t, "array", usersSchema.Type)
	require.Equal(t, "list of users", usersSchema.Description)

	// Verify array items nested object
	require.NotNil(t, usersSchema.Items, "array items should be parsed")
	require.Equal(t, "object", usersSchema.Items.Type)
	require.Equal(t, "user object", usersSchema.Items.Description)

	// Verify array item object properties
	require.NotNil(t, usersSchema.Items.Properties, "array item object properties should be parsed")
	require.Len(t, usersSchema.Items.Properties, 2, "array item object should have 2 properties")

	idSchema := usersSchema.Items.Properties["id"]
	require.NotNil(t, idSchema)
	require.Equal(t, "integer", idSchema.Type)
	require.Equal(t, "user id", idSchema.Description)

	nameSchema := usersSchema.Items.Properties["name"]
	require.NotNil(t, nameSchema)
	require.Equal(t, "string", nameSchema.Type)
	require.Equal(t, "user name", nameSchema.Description)

	// Verify required fields
	require.ElementsMatch(t, []string{"id", "name"}, usersSchema.Items.Required)
}
