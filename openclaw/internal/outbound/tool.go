//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package outbound

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolMessage = "message"
)

// Tool sends plain text messages through OpenClaw channels.
type Tool struct {
	router *Router
}

// NewTool creates a message tool backed by the outbound router.
func NewTool(router *Router) *Tool {
	return &Tool{router: router}
}

func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolMessage,
		Description: "Send a message through OpenClaw channels. " +
			"If channel/target are omitted, it uses the current " +
			"chat session when possible.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"text"},
			Properties: map[string]*tool.Schema{
				"text": {
					Type: "string",
					Description: "Message text to send. Use the " +
						"current chat by default.",
				},
				"channel": {
					Type: "string",
					Description: "Optional channel id. When " +
						"omitted, the tool uses runtime or " +
						"session context.",
				},
				"target": {
					Type: "string",
					Description: "Optional channel-specific " +
						"target. Telegram supports current " +
						"session ids, <chatID>, or " +
						"<chatID>:topic:<topicID>.",
				},
			},
		},
	}
}

type toolInput struct {
	Text    string `json:"text"`
	Channel string `json:"channel,omitempty"`
	Target  string `json:"target,omitempty"`
}

func (t *Tool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.router == nil {
		return nil, fmt.Errorf("message tool is not configured")
	}

	var in toolInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	target, err := ResolveTarget(ctx, DeliveryTarget{
		Channel: in.Channel,
		Target:  in.Target,
	})
	if err != nil {
		return nil, err
	}

	if err := t.router.SendText(ctx, target, in.Text); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"channel": target.Channel,
		"target":  target.Target,
	}, nil
}

var _ tool.CallableTool = (*Tool)(nil)
