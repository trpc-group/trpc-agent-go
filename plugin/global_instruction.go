//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package plugin

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultGlobalInstructionPluginName = "global_instruction"
	doubleNewline                      = "\n\n"
)

// GlobalInstruction prepends a system message to every model request.
//
// This is useful for enforcing organization-wide policies or shared behaviors
// without repeating configuration on each Agent.
type GlobalInstruction struct {
	name        string
	instruction string
}

// NewGlobalInstruction creates a GlobalInstruction plugin with a default
// name.
func NewGlobalInstruction(instruction string) *GlobalInstruction {
	return NewNamedGlobalInstruction(
		defaultGlobalInstructionPluginName,
		instruction,
	)
}

// NewNamedGlobalInstruction creates a GlobalInstruction plugin with a custom
// name. Names must be unique per Runner.
func NewNamedGlobalInstruction(
	name string,
	instruction string,
) *GlobalInstruction {
	if name == "" {
		name = defaultGlobalInstructionPluginName
	}
	return &GlobalInstruction{name: name, instruction: instruction}
}

// Name implements Plugin.
func (p *GlobalInstruction) Name() string { return p.name }

// Register implements Plugin.
func (p *GlobalInstruction) Register(r *Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeModel(p.beforeModel)
}

func (p *GlobalInstruction) beforeModel(
	_ context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if p == nil || args == nil || args.Request == nil {
		return nil, nil
	}
	applyGlobalInstruction(args.Request, p.instruction)
	return nil, nil
}

func applyGlobalInstruction(req *model.Request, instr string) {
	if req == nil {
		return
	}
	instr = strings.TrimSpace(instr)
	if instr == "" {
		return
	}

	if len(req.Messages) == 0 {
		req.Messages = []model.Message{model.NewSystemMessage(instr)}
		return
	}

	if req.Messages[0].Role == model.RoleSystem {
		if req.Messages[0].Content == "" {
			req.Messages[0].Content = instr
			return
		}
		req.Messages[0].Content = instr + doubleNewline + req.Messages[0].Content
		return
	}

	req.Messages = append(
		[]model.Message{model.NewSystemMessage(instr)},
		req.Messages...,
	)
}
