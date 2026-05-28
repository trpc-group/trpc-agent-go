//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates tool metadata and per-run permission policy.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultModelName  = "deepseek-v4-flash"
	appName           = "tool-policy-example"
	sessionIDPrefix   = "tool-policy"
	toolReadInventory = "read_inventory"
	toolSetInventory  = "set_inventory"
	roleViewer        = "viewer"
	roleOperator      = "operator"
	roleAdmin         = "admin"
	approvalReason    = "destructive inventory changes require an admin role"
	deniedReason      = "unknown role cannot execute tools"
	viewerDenyReason  = "viewer role cannot change inventory"
	itemNotebook      = "notebook"
	itemPen           = "pen"
	notebookCount     = 12
	penCount          = 30
)

var (
	modelName = flag.String("model", defaultModelName, "Name of the model to use")
	role      = flag.String("role", roleOperator, "User role: viewer, operator, or admin")
)

type inventoryItem struct {
	Name  string `json:"name" jsonschema:"description=Inventory item name"`
	Count int    `json:"count" jsonschema:"description=Inventory count"`
}

type metadataTool struct {
	tool.CallableTool
	metadata tool.ToolMetadata
}

func (m metadataTool) ToolMetadata() tool.ToolMetadata {
	return m.metadata
}

type example struct {
	runner    runner.Runner
	role      string
	sessionID string
	inventory map[string]int
}

func main() {
	flag.Parse()

	ex := &example{
		role:      *role,
		sessionID: fmt.Sprintf("%s-%d", sessionIDPrefix, time.Now().Unix()),
		inventory: map[string]int{
			itemNotebook: notebookCount,
			itemPen:      penCount,
		},
	}
	if err := ex.run(context.Background()); err != nil {
		log.Fatalf("tool policy example failed: %v", err)
	}
}

func (e *example) run(ctx context.Context) error {
	if err := e.setup(); err != nil {
		return err
	}
	defer e.runner.Close()

	fmt.Println("Tool metadata and permission policy example")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Role: %s\n", e.role)
	fmt.Println()
	fmt.Println("Try:")
	fmt.Println("  - read the inventory")
	fmt.Println("  - set notebook count to 8")
	fmt.Println("  - set pen count to 10")
	fmt.Println("Type /exit to quit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" {
			return nil
		}
		if err := e.send(ctx, text); err != nil {
			fmt.Printf("error: %v\n", err)
		}
	}
	return scanner.Err()
}

func (e *example) setup() error {
	modelInstance := openai.New(*modelName)
	ag := llmagent.New(
		"inventory-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(
			"Help the user inspect and update a small inventory. "+
				"If a tool result says approval is required, explain that the change was not applied.",
		),
		llmagent.WithTools(e.tools()),
	)
	e.runner = runner.NewRunner(
		appName,
		ag,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	return nil
}

func (e *example) tools() []tool.Tool {
	readTool := function.NewFunctionTool(
		e.readInventory,
		function.WithName(toolReadInventory),
		function.WithDescription("Read the current inventory counts."),
	)
	setTool := function.NewFunctionTool(
		e.setInventory,
		function.WithName(toolSetInventory),
		function.WithDescription("Set the count for one inventory item."),
	)
	return []tool.Tool{
		metadataTool{
			CallableTool: readTool,
			metadata: tool.ToolMetadata{
				ReadOnly:        true,
				ConcurrencySafe: true,
				SearchOrRead:    true,
			},
		},
		metadataTool{
			CallableTool: setTool,
			metadata: tool.ToolMetadata{
				Destructive: true,
			},
		},
	}
}

func (e *example) readInventory(_ context.Context, _ struct{}) (map[string]int, error) {
	out := make(map[string]int, len(e.inventory))
	for name, count := range e.inventory {
		out[name] = count
	}
	return out, nil
}

func (e *example) setInventory(_ context.Context, item inventoryItem) (map[string]any, error) {
	e.inventory[item.Name] = item.Count
	return map[string]any{
		"updated": item.Name,
		"count":   item.Count,
	}, nil
}

func (e *example) send(ctx context.Context, text string) error {
	events, err := e.runner.Run(
		ctx,
		e.role,
		e.sessionID,
		model.NewUserMessage(text),
		agent.WithToolPermissionPolicyFunc(e.permissionPolicy),
	)
	if err != nil {
		return err
	}
	for ev := range events {
		printEvent(ev)
	}
	return nil
}

func (e *example) permissionPolicy(
	_ context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	switch e.role {
	case roleAdmin:
		return tool.AllowPermission(), nil
	case roleOperator:
		if req.Metadata.Destructive {
			return tool.AskPermission(approvalReason), nil
		}
		return tool.AllowPermission(), nil
	case roleViewer:
		if req.Metadata.Destructive {
			return tool.DenyPermission(viewerDenyReason), nil
		}
		return tool.AllowPermission(), nil
	default:
		return tool.DenyPermission(deniedReason), nil
	}
}

func printEvent(ev *event.Event) {
	if ev == nil || ev.Response == nil {
		return
	}
	for _, choice := range ev.Response.Choices {
		if choice.Message.Role == model.RoleAssistant &&
			choice.Message.Content != "" {
			fmt.Printf("Assistant: %s\n", choice.Message.Content)
		}
		if choice.Message.Role == model.RoleTool &&
			choice.Message.Content != "" {
			fmt.Printf("Tool result: %s\n", choice.Message.Content)
		}
	}
}
