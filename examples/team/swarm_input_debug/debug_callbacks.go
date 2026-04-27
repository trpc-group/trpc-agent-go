//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

type requestMeta struct {
	AgentName       string
	InvocationID    string
	ParentID        string
	Branch          string
	FilterKey       string
	RequestID       string
	SessionID       string
	InvocationInput string
	InvocationRole  model.Role
	ModelName       string
}

func newModelDebugCallbacks(limit int) *model.Callbacks {
	return model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		meta := currentRequestMeta(ctx)
		fmt.Printf("\n\n=== BeforeModel agent=%s model=%s ===\n", meta.AgentName, meta.ModelName)
		fmt.Printf("invocation=%s parent=%s request=%s session=%s\n", meta.InvocationID, emptyAsDash(meta.ParentID), meta.RequestID, meta.SessionID)
		fmt.Printf("branch=%s filter=%s invocation_message_role=%s invocation_message=%q\n", meta.Branch, meta.FilterKey, meta.InvocationRole, clip(meta.InvocationInput, limit))
		if args == nil || args.Request == nil {
			fmt.Println("request=<nil>")
			fmt.Printf("=== End BeforeModel agent=%s ===\n", meta.AgentName)
			return nil, nil
		}
		fmt.Printf("generation stream=%t max_tokens=%s temperature=%s\n", args.Request.Stream, intPtrString(args.Request.MaxTokens), floatPtrString(args.Request.Temperature))
		printTools(args.Request.Tools)
		printMessages(args.Request.Messages, limit)
		fmt.Printf("=== End BeforeModel agent=%s ===\n", meta.AgentName)
		return nil, nil
	})
}

func newToolDebugCallbacks(limit int) *tool.Callbacks {
	return tool.NewCallbacks().RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		if args == nil || args.ToolName != transfer.TransferToolName {
			return nil, nil
		}
		meta := currentRequestMeta(ctx)
		fmt.Printf("\n\n=== BeforeTool agent=%s tool=%s ===\n", meta.AgentName, args.ToolName)
		fmt.Printf("args=%s\n", prettyJSONBytes(args.Arguments, limit))
		fmt.Printf("=== End BeforeTool agent=%s tool=%s ===\n", meta.AgentName, args.ToolName)
		return nil, nil
	})
}

func printProviderRequestJSON(limit int) openai.ChatRequestJSONCallbackFunc {
	return func(ctx context.Context, raw []byte, marshalErr error) {
		meta := currentRequestMeta(ctx)
		fmt.Printf("\n\n=== ProviderJSON agent=%s model=%s ===\n", meta.AgentName, meta.ModelName)
		if marshalErr != nil {
			fmt.Printf("marshal_error=%v\n", marshalErr)
			fmt.Printf("=== End ProviderJSON agent=%s ===\n", meta.AgentName)
			return
		}
		fmt.Println(prettyJSONBytes(raw, limit))
		fmt.Printf("=== End ProviderJSON agent=%s ===\n", meta.AgentName)
	}
}

func currentRequestMeta(ctx context.Context) requestMeta {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return requestMeta{AgentName: "unknown"}
	}
	parentID := ""
	if parent := inv.GetParentInvocation(); parent != nil {
		parentID = parent.InvocationID
	}
	sessionID := ""
	if inv.Session != nil {
		sessionID = inv.Session.ID
	}
	modelName := ""
	if inv.Model != nil {
		modelName = inv.Model.Info().Name
	}
	return requestMeta{
		AgentName:       inv.AgentName,
		InvocationID:    inv.InvocationID,
		ParentID:        parentID,
		Branch:          inv.Branch,
		FilterKey:       inv.GetEventFilterKey(),
		RequestID:       inv.RunOptions.RequestID,
		SessionID:       sessionID,
		InvocationInput: inv.Message.Content,
		InvocationRole:  inv.Message.Role,
		ModelName:       modelName,
	}
}
