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
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/modelspec"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/numconv"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/outputformat"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/toolspec"
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
//   - model_spec: Resolved model specification used by the framework to construct a model instance
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
//	    "model_spec": {
//	      "provider": "openai",
//	      "model_name": "deepseek-chat",
//	      "base_url": "https://api.deepseek.com/v1",
//	      "api_key": "env:OPENAI_API_KEY"
//	    },
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
				Name:        "model_spec",
				DisplayName: "Model Spec",
				Description: "Resolved model specification used by the framework to construct a concrete model instance.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    true,
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
				Description: "List of tool specifications available to the agent (unified ToolSpec format).",
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
				Name:        "planner",
				DisplayName: "Planner",
				Description: "Optional planner configuration. When specified, enables planning capabilities for this agent. Type can be 'react' (explicit planning tags) or 'builtin' (model's native thinking).",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
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
	specRaw, ok := config["model_spec"]
	if !ok || specRaw == nil {
		return fmt.Errorf("model_spec is required")
	}
	if _, err := modelspec.Parse(specRaw); err != nil {
		return err
	}

	// Validate instruction if present
	if instruction, ok := config["instruction"]; ok {
		if _, ok := instruction.(string); !ok {
			return fmt.Errorf("instruction must be a string")
		}
	}

	// Validate tools if present - use unified toolspec
	if tools, ok := config["tools"]; ok {
		if _, err := toolspec.ParseTools(tools); err != nil {
			return err
		}
	}

	// Validate output_format if present
	if outputFormat, ok := config["output_format"]; ok {
		if _, err := outputformat.Parse(outputFormat); err != nil {
			return err
		}
	}

	// Validate temperature if present
	if temperature, ok := config["temperature"]; ok {
		temp, err := numconv.Float64(temperature, "temperature")
		if err != nil {
			return err
		}
		if temp < 0 || temp > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
	}

	// Validate max_tokens if present
	if maxTokens, ok := config["max_tokens"]; ok {
		tokens, err := numconv.Int(maxTokens, "max_tokens")
		if err != nil {
			return err
		}
		if tokens <= 0 {
			return fmt.Errorf("max_tokens must be positive")
		}
	}

	// Validate top_p if present
	if topP, ok := config["top_p"]; ok {
		tp, err := numconv.Float64(topP, "top_p")
		if err != nil {
			return err
		}
		if tp < 0 || tp > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
	}

	return nil
}

// NewLLMAgentComponent creates a new LLMAgentComponent instance.
func NewLLMAgentComponent() *LLMAgentComponent {
	return &LLMAgentComponent{}
}
