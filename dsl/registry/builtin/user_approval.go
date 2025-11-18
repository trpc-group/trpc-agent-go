//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package builtin

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Auto-register UserApproval component at package init time.
	registry.MustRegister(&UserApprovalComponent{})
}

// UserApprovalComponent represents a human-in-the-loop approval step.
// It is implemented via graph.Interrupt at the DSL/compiler layer and
// should not be executed directly via this component's Execute method.
//
// The component exposes:
//   - Config:
//     - message: string (required) – prompt text shown to the user.
//   - Outputs:
//     - approval_result: string – normalized decision, e.g. "approve" or "reject".
//     - last_response:  string – mirrors the message for consistency with other nodes.
type UserApprovalComponent struct{}

// Metadata returns the component metadata for builtin.user_approval.
func (c *UserApprovalComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.user_approval",
		DisplayName: "User Approval",
		Description: "Human-in-the-loop approval step implemented via graph.Interrupt",
		Category:    "Control",
		Version:     "1.0.0",
		Inputs:      []registry.ParameterSchema{},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "approval_result",
				DisplayName: "Approval Result",
				Description: "Normalized approval decision (e.g. \"approve\" or \"reject\")",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
			},
			{
				Name:        graph.StateKeyLastResponse,
				DisplayName: "Last Response",
				Description: "Echo of the approval message for downstream use",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
			},
		},
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "message",
				DisplayName: "Message",
				Description: "Approval message shown to the user",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
				Placeholder: "Does this work for you?",
			},
			{
				Name:        "auto_approve",
				DisplayName: "Auto Approve",
				Description: "When true, skip interrupt and treat as approved (for demos/tests)",
				Type:        "bool",
				TypeID:      "boolean",
				Kind:        "boolean",
				GoType:      reflect.TypeOf(false),
				Required:    false,
				Default:     false,
			},
		},
	}
}

// Execute should not be called for builtin.user_approval.
// This node is handled specially by the DSL compiler using graph.Interrupt.
func (c *UserApprovalComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	return nil, fmt.Errorf("builtin.user_approval.Execute should not be called directly - node is handled by compiler")
}

// Validate validates the component configuration.
func (c *UserApprovalComponent) Validate(config registry.ComponentConfig) error {
	message, ok := config["message"].(string)
	if !ok {
		return fmt.Errorf("message must be a string")
	}
	if message == "" {
		return fmt.Errorf("message cannot be empty")
	}
	if auto, ok := config["auto_approve"]; ok {
		if _, ok := auto.(bool); !ok {
			return fmt.Errorf("auto_approve must be a boolean")
		}
	}
	return nil
}

// NewUserApprovalComponent creates a new UserApprovalComponent instance.
func NewUserApprovalComponent() *UserApprovalComponent {
	return &UserApprovalComponent{}
}
