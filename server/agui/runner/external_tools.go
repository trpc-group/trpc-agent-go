//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	errAGUIToolNameRequired        = "agui tool name is required"
	errAGUIToolNameRequiredAt      = "agui tool[%d]: %s"
	errConvertAGUIToolParameters   = "convert agui tool[%d] %q parameters: %w"
	errMarshalAGUIToolParameters   = "marshal agui tool parameters"
	errUnmarshalAGUIToolParameters = "unmarshal agui tool parameters"
	jsonSchemaTypeObject           = "object"
)

func appendExternalToolRunOption(
	opts []agent.RunOption,
	input *adapter.RunAgentInput,
) ([]agent.RunOption, error) {
	externalTools, err := externalToolsFromRunAgentInput(input)
	if err != nil {
		return nil, err
	}
	if len(externalTools) == 0 {
		return opts, nil
	}
	return append(opts, agent.WithExternalTools(externalTools)), nil
}

func externalToolsFromRunAgentInput(
	input *adapter.RunAgentInput,
) ([]agenttool.Tool, error) {
	if input == nil || len(input.Tools) == 0 {
		return nil, nil
	}
	tools := make([]agenttool.Tool, 0, len(input.Tools))
	for i, inputTool := range input.Tools {
		if inputTool.Name == "" {
			return nil, fmt.Errorf(
				errAGUIToolNameRequiredAt,
				i,
				errAGUIToolNameRequired,
			)
		}
		schema, err := aguiToolParametersToSchema(inputTool.Parameters)
		if err != nil {
			return nil, fmt.Errorf(
				errConvertAGUIToolParameters,
				i,
				inputTool.Name,
				err,
			)
		}
		tools = append(tools, &declarationOnlyTool{
			declaration: &agenttool.Declaration{
				Name:        inputTool.Name,
				Description: inputTool.Description,
				InputSchema: schema,
			},
		})
	}
	return tools, nil
}

func aguiToolParametersToSchema(params any) (*agenttool.Schema, error) {
	if params == nil {
		return &agenttool.Schema{Type: jsonSchemaTypeObject}, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errMarshalAGUIToolParameters, err)
	}
	var schema agenttool.Schema
	if err := json.Unmarshal(b, &schema); err != nil {
		return nil, fmt.Errorf("%s: %w", errUnmarshalAGUIToolParameters, err)
	}
	return &schema, nil
}

type declarationOnlyTool struct {
	declaration *agenttool.Declaration
}

func (t *declarationOnlyTool) Declaration() *agenttool.Declaration {
	return t.declaration
}
