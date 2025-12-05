//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func init() {
	// Auto-register LLMAgent component at package init time
	registry.MustRegister(&LLMAgentComponent{})
}

// LLMAgentComponent is a builtin component that dynamically creates and executes an LLMAgent.
// It allows front-end users to configure an LLMAgent directly in DSL without pre-registration.
//
// This component wraps the llmagent.New() constructor and supports common LLMAgent options:
//   - model_id: Logical model ID resolved by ModelProvider/ModelRegistry
//   - instruction: System prompt/instruction
//   - tools: List of tool names (from ToolRegistry)
//   - output_format: Output configuration { type: text|json, schema } for structured output
//   - temperature, max_tokens, top_p: Generation parameters
//
// Example DSL:
//
//	{
//	  "id": "classification_agent",
//	  "component": {
//	    "type": "component",
//	    "ref": "builtin.llmagent"
//	  },
//	  "config": {
//	    "model_id": "gpt-4-turbo",
//	    "instruction": "You are a classification agent. Classify user intent into categories.",
//	    "tools": ["search", "calculator"],
//	    "temperature": 0.7,
//	    "max_tokens": 1000
//	  }
//	}
type LLMAgentComponent struct{}

// Metadata returns the component metadata.
func (c *LLMAgentComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.llmagent",
		DisplayName: "LLM Agent",
		Description: "Dynamically creates and executes an LLMAgent with configurable model, instruction, and tools",
		Category:    "Agent",
		Version:     "1.0.0",
		// LLMAgent does not consume additional named state inputs beyond the
		// built-in graph fields (messages/user_input/session). Those are added
		// by SchemaInference.addBuiltinFields, so Inputs can remain empty here.
		Inputs: []registry.ParameterSchema{},
		Outputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyLastResponse,
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Last response from the LLM agent",
			},
			{
				Name:        graph.StateKeyMessages,
				Type:        "[]model.Message",
				TypeID:      "graph.messages",
				Kind:        "array",
				GoType:      reflect.TypeOf([]model.Message{}),
				Description: "Conversation messages",
				Reducer:     "message",
			},
		},
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "model_id",
				DisplayName: "Model ID",
				Description: "Optional logical model identifier (kept for compatibility and observability).",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Placeholder: "deepseek-chat",
			},
			{
				Name:        "model_spec",
				DisplayName: "Model Spec",
				Description: "Resolved model specification used by the framework to construct a concrete model instance.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "instruction",
				DisplayName: "Instruction",
				Description: "System prompt / instruction for the agent",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Placeholder: "You are a helpful assistant",
			},
			{
				Name:        "tools",
				DisplayName: "Tools",
				Description: "List of tool names to make available to the agent (from ToolRegistry or MCP toolsets).",
				Type:        "[]string",
				TypeID:      "array.string",
				Kind:        "array",
				GoType:      reflect.TypeOf([]string{}),
				Required:    false,
				Placeholder: "search, calculator",
			},
			{
				Name:        "mcp_tools",
				DisplayName: "MCP Tools",
				Description: "List of MCP server configurations attached to this agent (server_url/allowed_tools/transport/etc.). Each entry corresponds to one MCP server and exposes one or more tools from that server to the agent.",
				Type:        "[]map[string]any",
				TypeID:      "array.object",
				Kind:        "array",
				GoType:      reflect.TypeOf([]map[string]any{}),
				Required:    false,
			},
			{
				Name:        "output_format",
				DisplayName: "Output Format",
				Description: "Controls how the agent returns its response. When type == \"json\", schema contains the JSON Schema for structured output and the agent writes parsed JSON to node_structured[<id>].output_parsed.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "temperature",
				DisplayName: "Temperature",
				Description: "Sampling temperature (0.0 to 2.0, default: model default)",
				Type:        "float64",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(float64(0)),
				Required:    false,
				Default:     0.7,
				Validation: &registry.ValidationRules{
					Min: floatPtr(0.0),
					Max: floatPtr(2.0),
				},
			},
			{
				Name:        "max_tokens",
				DisplayName: "Max Tokens",
				Description: "Maximum tokens to generate (default: model default)",
				Type:        "int",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(0),
				Required:    false,
			},
			{
				Name:        "top_p",
				DisplayName: "Top P",
				Description: "Nucleus sampling parameter (0.0 to 1.0, default: model default)",
				Type:        "float64",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(float64(0)),
				Required:    false,
				Validation: &registry.ValidationRules{
					Min: floatPtr(0.0),
					Max: floatPtr(1.0),
				},
			},
			{
				Name:        "stop",
				DisplayName: "Stop Sequences",
				Description: "Optional array of stop sequences where the model will stop generating further tokens.",
				Type:        "[]string",
				TypeID:      "array.string",
				Kind:        "array",
				GoType:      reflect.TypeOf([]string{}),
				Required:    false,
			},
			{
				Name:        "presence_penalty",
				DisplayName: "Presence Penalty",
				Description: "Penalizes new tokens based on whether they appear in the text so far (OpenAI presence_penalty).",
				Type:        "float64",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(float64(0)),
				Required:    false,
			},
			{
				Name:        "frequency_penalty",
				DisplayName: "Frequency Penalty",
				Description: "Penalizes new tokens based on their frequency in the text so far (OpenAI frequency_penalty).",
				Type:        "float64",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(float64(0)),
				Required:    false,
			},
			{
				Name:        "reasoning_effort",
				DisplayName: "Reasoning Effort",
				Description: "Limits reasoning effort for reasoning models (e.g., \"low\", \"medium\", \"high\").",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
			},
			{
				Name:        "thinking_enabled",
				DisplayName: "Thinking Enabled",
				Description: "Enable thinking mode for supported providers (e.g., Claude/Gemini via OpenAI API).",
				Type:        "bool",
				TypeID:      "boolean",
				Kind:        "boolean",
				GoType:      reflect.TypeOf(false),
				Required:    false,
				Default:     false,
			},
			{
				Name:        "thinking_tokens",
				DisplayName: "Thinking Tokens",
				Description: "Maximum number of tokens to spend in thinking mode when thinking_enabled is true.",
				Type:        "int",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(0),
				Required:    false,
			},
			{
				Name:        "stream",
				DisplayName: "Stream",
				Description: "Enable token streaming",
				Type:        "bool",
				TypeID:      "boolean",
				Kind:        "boolean",
				GoType:      reflect.TypeOf(false),
				Required:    false,
				Default:     false,
			},
			{
				Name:        "description",
				DisplayName: "Description",
				Description: "Description of the agent",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
			},
		},
	}
}

// Execute should not be called for builtin.llmagent.
// This component is handled specially by the compiler via createLLMAgentNodeFunc.
func (c *LLMAgentComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	return nil, fmt.Errorf("builtin.llmagent.Execute should not be called directly - component is handled by compiler")
}

// Validate validates the component configuration.
func (c *LLMAgentComponent) Validate(config registry.ComponentConfig) error {
	// Validate model_spec (preferred) or model_id (compatibility).
	if specRaw, hasSpec := config["model_spec"]; hasSpec && specRaw != nil {
		spec, ok := specRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("model_spec must be an object")
		}
		providerName, _ := spec["provider"].(string)
		modelName, _ := spec["model_name"].(string)
		apiKey, _ := spec["api_key"].(string)
		if strings.TrimSpace(providerName) == "" {
			return fmt.Errorf("model_spec.provider cannot be empty")
		}
		if strings.TrimSpace(modelName) == "" {
			return fmt.Errorf("model_spec.model_name cannot be empty")
		}
		if strings.TrimSpace(apiKey) == "" {
			return fmt.Errorf("model_spec.api_key cannot be empty")
		}
	} else if modelIDRaw, hasID := config["model_id"]; hasID {
		modelID, ok := modelIDRaw.(string)
		if !ok {
			return fmt.Errorf("model_id must be a string")
		}
		if strings.TrimSpace(modelID) == "" {
			return fmt.Errorf("model_id cannot be empty")
		}
	} else {
		return fmt.Errorf("either model_spec or model_id is required")
	}

	// Validate instruction if present
	if instruction, ok := config["instruction"]; ok {
		if _, ok := instruction.(string); !ok {
			return fmt.Errorf("instruction must be a string")
		}
	}

	// Validate tools if present
	if tools, ok := config["tools"]; ok {
		switch toolsSlice := tools.(type) {
		case []interface{}:
			for i, tool := range toolsSlice {
				if _, ok := tool.(string); !ok {
					return fmt.Errorf("tools[%d] must be a string", i)
				}
			}
		case []string:
			// ok
		default:
			return fmt.Errorf("tools must be an array")
		}
	}

	// Validate output_format if present
	if outputFormat, ok := config["output_format"]; ok {
		ofMap, ok := outputFormat.(map[string]any)
		if !ok {
			return fmt.Errorf("output_format must be an object")
		}

		if t, ok := ofMap["type"]; ok {
			typeStr, ok := t.(string)
			if !ok {
				return fmt.Errorf("output_format.type must be a string when present")
			}
			typeStr = strings.TrimSpace(typeStr)
			if typeStr != "" && typeStr != "text" && typeStr != "json" {
				return fmt.Errorf("output_format.type must be one of: text, json")
			}
		}

		if schema, ok := ofMap["schema"]; ok {
			if _, ok := schema.(map[string]any); !ok {
				return fmt.Errorf("output_format.schema must be an object when present")
			}
		}
	}

	// Validate temperature if present
	if temperature, ok := config["temperature"]; ok {
		var temp float64
		switch v := temperature.(type) {
		case float64:
			temp = v
		case int:
			temp = float64(v)
		default:
			return fmt.Errorf("temperature must be a number")
		}
		if temp < 0 || temp > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
	}

	// Validate max_tokens if present
	if maxTokens, ok := config["max_tokens"]; ok {
		var tokens int
		switch v := maxTokens.(type) {
		case int:
			tokens = v
		case float64:
			maxInt := int(^uint(0) >> 1)
			if v > float64(maxInt) {
				return fmt.Errorf("max_tokens is too large")
			}
			if v != float64(int(v)) {
				return fmt.Errorf("max_tokens must be an integer")
			}
			tokens = int(v)
		case json.Number:
			n, err := v.Int64()
			if err != nil {
				return fmt.Errorf("max_tokens must be an integer")
			}
			maxInt := int64(^uint(0) >> 1)
			if n > maxInt {
				return fmt.Errorf("max_tokens is too large")
			}
			tokens = int(n)
		default:
			return fmt.Errorf("max_tokens must be an integer")
		}
		if tokens <= 0 {
			return fmt.Errorf("max_tokens must be positive")
		}
	}

	// Validate top_p if present
	if topP, ok := config["top_p"]; ok {
		var tp float64
		switch v := topP.(type) {
		case float64:
			tp = v
		case int:
			tp = float64(v)
		default:
			return fmt.Errorf("top_p must be a number")
		}
		if tp < 0 || tp > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
	}

	// Validate mcp_tools if present
	if mcpTools, ok := config["mcp_tools"]; ok {
		mcpToolsSlice, ok := mcpTools.([]interface{})
		if !ok {
			return fmt.Errorf("mcp_tools must be an array")
		}
		for i, mcpTool := range mcpToolsSlice {
			mcpToolConfig, ok := mcpTool.(map[string]interface{})
			if !ok {
				return fmt.Errorf("mcp_tools[%d] must be an object", i)
			}

			// Validate server_url (required)
			serverURL, ok := mcpToolConfig["server_url"].(string)
			if !ok || serverURL == "" {
				return fmt.Errorf("mcp_tools[%d].server_url is required and must be a non-empty string", i)
			}

			// Validate transport (optional, defaults to streamable_http)
			if transportRaw, ok := mcpToolConfig["transport"]; ok {
				transport, ok := transportRaw.(string)
				if !ok {
					return fmt.Errorf("mcp_tools[%d].transport must be a string when present", i)
				}
				if transport != "streamable_http" && transport != "sse" {
					return fmt.Errorf("mcp_tools[%d].transport must be one of: streamable_http, sse", i)
				}
			}

			// Validate allowed_tools if present
			if allowed, ok := mcpToolConfig["allowed_tools"]; ok {
				switch v := allowed.(type) {
				case []interface{}:
					for j, elem := range v {
						if _, ok := elem.(string); !ok {
							return fmt.Errorf("mcp_tools[%d].allowed_tools[%d] must be a string", i, j)
						}
					}
				case []string:
					// ok
				default:
					return fmt.Errorf("mcp_tools[%d].allowed_tools must be an array of strings", i)
				}
			}

			// Validate require_approval if present
			if ra, ok := mcpToolConfig["require_approval"]; ok {
				if raStr, ok := ra.(string); ok {
					if raStr != "always" && raStr != "never" && raStr != "auto" {
						return fmt.Errorf("mcp_tools[%d].require_approval must be one of: always, never, auto", i)
					}
				} else {
					return fmt.Errorf("mcp_tools[%d].require_approval must be a string", i)
				}
			}
		}
	}

	return nil
}

// NewLLMAgentComponent creates a new LLMAgentComponent instance.
func NewLLMAgentComponent() *LLMAgentComponent {
	return &LLMAgentComponent{}
}
