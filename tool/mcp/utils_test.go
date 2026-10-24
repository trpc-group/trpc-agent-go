//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestConvertMCPSchema_TypeArrays(t *testing.T) {
	for _, types := range [][]any{
		{"null", "integer"},
		{"integer", "null"},
		{"null", "string"},
		{"string", "number"},
		{"null", "array", "object"},
		{"null"},
		{"boolean"},
		{"null", 1, "integer"},
	} {
		t.Run(typeArrayName(types), func(t *testing.T) {
			input := map[string]any{"type": types, "description": "value"}
			before, err := json.Marshal(input)
			require.NoError(t, err)
			encoded, err := json.Marshal(convertMCPSchemaToSchema(input))
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(encoded, &wire))
			require.NotContains(t, wire, "type")
			var branches []any
			for _, value := range types {
				name, ok := value.(string)
				if !ok {
					continue
				}
				branches = append(branches, map[string]any{"type": name})
			}
			require.Equal(t, branches, wire["anyOf"])
			require.Equal(t, "value", wire["description"])
			after, err := json.Marshal(input)
			require.NoError(t, err)
			require.Equal(t, before, after, "conversion must not mutate remote schemas")
		})
	}
}

func typeArrayName(types []any) string {
	name := ""
	for i, value := range types {
		if i > 0 {
			name += "_"
		}
		text, ok := value.(string)
		if !ok {
			name += "nonstring"
			continue
		}
		name += text
	}
	return name
}

func TestConvertMCPSchema_TypeArraysAtSupportedPositions(t *testing.T) {
	requireConvertedJSON(t, `{
		"type": "object",
		"additionalProperties": false,
		"default": 1,
		"enum": ["root"],
		"items": {"type": ["null", "string"]},
		"anyOf": [{"type": "integer", "pattern": "^ignored$"}],
		"required": ["anchor"],
		"properties": {
			"anchor": {"type": "string"},
			"limit": {"type": ["null", "integer"], "default": 50, "enum": [null, 50, 100]},
			"title": {"type": ["integer", "null"]},
			"options": {
				"type": ["object", "null"],
				"required": ["name"],
				"properties": {"name": {"type": ["string", "null"], "pattern": "^[a-z]+$"}}
			},
			"rows": {
				"type": ["array", "null"],
				"items": {
					"type": ["object", "null"],
					"default": "lost-on-items",
					"properties": {"count": {"type": ["null", "integer"]}}
				}
			},
			"reference": {"$ref": "#/$defs/Value"}
		},
		"$defs": {
			"Value": {
				"type": ["null", "array"],
				"items": {"type": ["null", "string"]},
				"default": "lost",
				"additionalProperties": false
			}
		}
	}`, `{
		"type": "object",
		"required": ["anchor"],
		"properties": {
			"anchor": {"type": "string"},
			"limit": {
				"anyOf": [{"type": "null"}, {"type": "integer"}],
				"default": 50,
				"enum": [null, 50, 100]
			},
			"options": {
				"anyOf": [{"type": "object"}, {"type": "null"}],
				"required": ["name"],
				"properties": {
					"name": {
						"anyOf": [{"type": "string"}, {"type": "null"}],
						"pattern": "^[a-z]+$"
					}
				}
			},
			"reference": {"$ref": "#/$defs/Value"},
			"rows": {
				"anyOf": [{"type": "array"}, {"type": "null"}],
				"items": {
					"anyOf": [{"type": "object"}, {"type": "null"}],
					"properties": {
						"count": {"anyOf": [{"type": "null"}, {"type": "integer"}]}
					}
				}
			},
			"title": {"anyOf": [{"type": "integer"}, {"type": "null"}]}
		},
		"$defs": {"Value": {"anyOf": [{"type": "null"}, {"type": "array"}]}}
	}`)
}

func TestConvertMCPSchema_BaselineSingleTypeJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "integer", input: `{"type":"integer"}`, want: `{"type":"integer"}`},
		{name: "string", input: `{"type":"string"}`, want: `{"type":"string"}`},
		{name: "boolean", input: `{"type":"boolean"}`, want: `{"type":"boolean"}`},
		{name: "number", input: `{"type":"number"}`, want: `{"type":"number"}`},
		{name: "object", input: `{"type":"object"}`, want: `{"type":"object"}`},
		{name: "array", input: `{"type":"array"}`, want: `{"type":"array"}`},
		{
			name: "nested properties defaults enums required items refs defs",
			input: `{
				"type": "object",
				"description": "root",
				"pattern": "^root$",
				"default": 1,
				"enum": ["a"],
				"items": {"type": "string", "default": "lost"},
				"additionalProperties": false,
				"anyOf": [{"type": "string", "pattern": "^[a-z]+$"}, {"type": "null"}],
				"required": ["name", "count"],
				"$ref": "#/$defs/Root",
				"$defs": {
					"Item": {
						"type": "object",
						"description": "item def",
						"default": "nope",
						"enum": ["x"],
						"items": {"type": "integer"},
						"additionalProperties": false,
						"anyOf": [{"type": "null"}],
						"properties": {
							"label": {"type": "string", "default": "n", "enum": ["n", "y"]}
						},
						"required": ["label"]
					}
				},
				"properties": {
					"name": {
						"type": "string",
						"description": "name",
						"pattern": "^[a-z]+$",
						"default": "ada",
						"enum": ["ada", "grace"],
						"anyOf": [{"type": "null"}]
					},
					"count": {"type": "integer"},
					"ok": {"type": "boolean", "default": true},
					"ratio": {"type": "number"},
					"meta": {
						"type": "object",
						"additionalProperties": {"type": "string", "default": "x"},
						"required": ["id"],
						"properties": {"id": {"type": "integer", "default": 7}}
					},
					"tags": {
						"type": "array",
						"items": {
							"type": "string",
							"description": "tag",
							"default": "lost-on-items",
							"enum": ["a"],
							"items": {"type": "number"},
							"additionalProperties": false,
							"anyOf": [{"type": "null"}],
							"pattern": "^[a-z]$"
						}
					},
					"ref": {"$ref": "#/$defs/Item", "type": "object"}
				}
			}`,
			want: `{
				"type": "object",
				"description": "root",
				"pattern": "^root$",
				"required": ["name", "count"],
				"properties": {
					"count": {"type": "integer"},
					"meta": {
						"type": "object",
						"required": ["id"],
						"properties": {"id": {"type": "integer", "default": 7}},
						"additionalProperties": {"default": "x", "type": "string"}
					},
					"name": {
						"type": "string",
						"description": "name",
						"pattern": "^[a-z]+$",
						"default": "ada",
						"enum": ["ada", "grace"]
					},
					"ok": {"type": "boolean", "default": true},
					"ratio": {"type": "number"},
					"ref": {"type": "object", "$ref": "#/$defs/Item"},
					"tags": {
						"type": "array",
						"items": {
							"type": "string",
							"description": "tag",
							"pattern": "^[a-z]$"
						}
					}
				},
				"$ref": "#/$defs/Root",
				"$defs": {
					"Item": {
						"type": "object",
						"description": "item def",
						"required": ["label"],
						"properties": {
							"label": {"type": "string", "default": "n", "enum": ["n", "y"]}
						}
					}
				}
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireConvertedJSON(t, tc.input, tc.want)
		})
	}
}

func requireConvertedJSON(t *testing.T, input, want string) {
	t.Helper()
	var decoded any
	require.NoError(t, json.Unmarshal([]byte(input), &decoded))
	before, err := json.Marshal(decoded)
	require.NoError(t, err)

	got, err := json.Marshal(convertMCPSchemaToSchema(decoded))
	require.NoError(t, err)
	after, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.Equal(t, before, after, "conversion must not mutate remote schemas")

	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, []byte(want)))
	require.Equal(t, compact.String(), string(got))
}

func TestConvertMCPSchema_Basic(t *testing.T) {
	mcpSchema := map[string]any{
		"type":        "object",
		"description": "test schema",
		"required":    []any{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string", "pattern": "^[a-z]+$"},
			"b": map[string]any{"type": "number", "description": "bbb"},
		},
	}

	s := convertMCPSchemaToSchema(mcpSchema)
	require.Equal(t, "object", s.Type)
	require.Equal(t, "test schema", s.Description)
	require.ElementsMatch(t, []string{"a", "b"}, s.Required)
	require.Equal(t, "string", s.Properties["a"].Type)
	require.Equal(t, "^[a-z]+$", s.Properties["a"].Pattern)
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

func TestConvertMCPSchema_RootPattern(t *testing.T) {
	mcpSchema := map[string]any{
		"type":    "string",
		"pattern": "^[a-z]+$",
	}

	schema := convertMCPSchemaToSchema(mcpSchema)
	require.Equal(t, "string", schema.Type)
	require.Equal(t, "^[a-z]+$", schema.Pattern)
}

func TestConvertMCPSchema_ValueConstraints(t *testing.T) {
	mcpSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"when":  map[string]any{"type": "string", "format": "date-time"},
			"score": map[string]any{"type": "number", "minimum": 0, "maximum": 100},
			"tags": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": 5,
				"items":    map[string]any{"type": "string"},
			},
			"point": map[string]any{
				"type": "array",
				"prefixItems": []any{
					map[string]any{"type": "number", "minimum": -90},
					map[string]any{"type": "number", "maximum": 180},
				},
			},
			"bad":  map[string]any{"type": "array", "minItems": 1.5, "maxItems": -1},
			"huge": map[string]any{"type": "array", "minItems": 1e20},
		},
	}

	s := convertMCPSchemaToSchema(mcpSchema)
	require.Equal(t, "date-time", s.Properties["when"].Format)

	score := s.Properties["score"]
	require.NotNil(t, score.Minimum)
	require.NotNil(t, score.Maximum)
	require.Equal(t, 0.0, *score.Minimum)
	require.Equal(t, 100.0, *score.Maximum)

	tags := s.Properties["tags"]
	require.NotNil(t, tags.MinItems)
	require.NotNil(t, tags.MaxItems)
	require.Equal(t, 1, *tags.MinItems)
	require.Equal(t, 5, *tags.MaxItems)

	point := s.Properties["point"]
	require.Len(t, point.PrefixItems, 2)
	require.Equal(t, -90.0, *point.PrefixItems[0].Minimum)
	require.Equal(t, 180.0, *point.PrefixItems[1].Maximum)

	require.Nil(t, s.Properties["bad"].MinItems)
	require.Nil(t, s.Properties["bad"].MaxItems)
	require.Nil(t, s.Properties["huge"].MinItems)
}

func TestConvertMCPSchema_PrefixItemsKeepPositions(t *testing.T) {
	s := convertMCPSchemaToSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"row": map[string]any{
				"type": "array",
				"prefixItems": []any{
					true,
					map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "integer", "minimum": 0},
					},
				},
			},
			"never": map[string]any{
				"type":        "array",
				"prefixItems": []any{false, map[string]any{"type": "string"}},
			},
		},
	})

	row := s.Properties["row"]
	require.Len(t, row.PrefixItems, 2)
	data, err := json.Marshal(row.PrefixItems[0])
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))
	require.Equal(t, "array", row.PrefixItems[1].Type)
	require.NotNil(t, row.PrefixItems[1].Items)
	require.Equal(t, "integer", row.PrefixItems[1].Items.Type)
	require.Equal(t, 0.0, *row.PrefixItems[1].Items.Minimum)

	require.Nil(t, s.Properties["never"].PrefixItems)
}

func TestConvertMCPSchema_RootValueConstraints(t *testing.T) {
	schema := convertMCPSchemaToSchema(map[string]any{
		"type":    "integer",
		"format":  "int32",
		"minimum": 1,
	})
	require.Equal(t, "int32", schema.Format)
	require.NotNil(t, schema.Minimum)
	require.Equal(t, 1.0, *schema.Minimum)
	require.Nil(t, schema.Maximum)
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

func TestNewMCPTool_WithOutputSchema(t *testing.T) {
	// Test OutputSchema conversion using the same approach as existing schema tests
	outputSchema := map[string]any{
		"type":        "object",
		"description": "Output schema for test tool",
		"required":    []any{"result", "success"},
		"properties": map[string]any{
			"result": map[string]any{
				"type":        "string",
				"description": "Operation result",
			},
			"success": map[string]any{
				"type":        "boolean",
				"description": "Success flag",
			},
		},
	}

	// Test the schema conversion function directly
	convertedSchema := convertMCPSchemaToSchema(outputSchema)

	// Verify the converted schema
	require.NotNil(t, convertedSchema)
	require.Equal(t, "object", convertedSchema.Type)
	require.Equal(t, "Output schema for test tool", convertedSchema.Description)
	require.Contains(t, convertedSchema.Properties, "result")
	require.Contains(t, convertedSchema.Properties, "success")
	require.Equal(t, "string", convertedSchema.Properties["result"].Type)
	require.Equal(t, "Operation result", convertedSchema.Properties["result"].Description)
	require.Equal(t, "boolean", convertedSchema.Properties["success"].Type)
	require.Equal(t, "Success flag", convertedSchema.Properties["success"].Description)
	require.ElementsMatch(t, []string{"result", "success"}, convertedSchema.Required)
}

func TestConvertMCPSchema_RefAndDefs(t *testing.T) {
	mcpSchema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"Address": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"street": map[string]any{"type": "string"},
					"city":   map[string]any{"type": "string"},
				},
				"required": []any{"street", "city"},
			},
		},
		"properties": map[string]any{
			"home_address": map[string]any{
				"$ref": "#/$defs/Address",
			},
			"work_address": map[string]any{
				"$ref": "#/$defs/Address",
			},
			"name": map[string]any{
				"type": "string",
			},
		},
		"required": []any{"name"},
	}

	schema := convertMCPSchemaToSchema(mcpSchema)

	require.Equal(t, "object", schema.Type)
	require.ElementsMatch(t, []string{"name"}, schema.Required)

	// Verify $defs are preserved.
	require.NotNil(t, schema.Defs)
	require.Contains(t, schema.Defs, "Address")
	addrDef := schema.Defs["Address"]
	require.Equal(t, "object", addrDef.Type)
	require.Contains(t, addrDef.Properties, "street")
	require.Contains(t, addrDef.Properties, "city")
	require.ElementsMatch(t, []string{"street", "city"}, addrDef.Required)

	// Verify $ref in properties are preserved.
	require.NotNil(t, schema.Properties["home_address"])
	require.Equal(t, "#/$defs/Address", schema.Properties["home_address"].Ref)
	require.NotNil(t, schema.Properties["work_address"])
	require.Equal(t, "#/$defs/Address", schema.Properties["work_address"].Ref)

	// Verify non-ref property still works.
	require.Equal(t, "string", schema.Properties["name"].Type)
}

func TestConvertMCPSchema_TopLevelRef(t *testing.T) {
	mcpSchema := map[string]any{
		"$ref": "#/$defs/MyType",
	}

	schema := convertMCPSchemaToSchema(mcpSchema)
	require.Equal(t, "#/$defs/MyType", schema.Ref)
}

func TestConvertDefs_InvalidEntriesIgnored(t *testing.T) {
	mcpSchema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"Address": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"street": map[string]any{"type": "string"},
					"city":   map[string]any{"type": "string"},
				},
				"required": []any{"street", "city"},
			},
			"Invalid": "not an object",
		},
	}

	schema := convertMCPSchemaToSchema(mcpSchema)

	require.NotNil(t, schema.Defs)
	require.Contains(t, schema.Defs, "Address")
	require.NotContains(t, schema.Defs, "Invalid")
	require.Equal(t, "object", schema.Defs["Address"].Type)
}
