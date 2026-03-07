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

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
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
		Description: "Send text and optional local files/media " +
			"through OpenClaw channels. If channel/target are " +
			"omitted, it uses the current chat session when " +
			"possible.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"text": {
					Type: "string",
					Description: "Message text to send. Use the " +
						"current chat by default. Optional when " +
						"files are provided.",
				},
				"files": {
					Type: "array",
					Items: &tool.Schema{
						Type: "string",
					},
					Description: "Optional local file paths, " +
						"host:// refs, artifact:// refs, or " +
						"workspace:// refs to send back to the " +
						"user. Telegram auto-picks document, " +
						"photo, audio, voice, or video upload " +
						"mode based on the file type.",
				},
				"file": {
					Type:        "string",
					Description: "Alias for a single file path.",
				},
				"media": {
					Type: "array",
					Items: &tool.Schema{
						Type: "string",
					},
					Description: "Alias for files.",
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
	Text    string   `json:"text"`
	File    string   `json:"file,omitempty"`
	Files   []string `json:"files,omitempty"`
	Media   []string `json:"media,omitempty"`
	Channel string   `json:"channel,omitempty"`
	Target  string   `json:"target,omitempty"`
}

func (t *Tool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.router == nil {
		return nil, fmt.Errorf("message tool is not configured")
	}

	var in toolInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	msg, err := buildOutboundMessage(in)
	if err != nil {
		return nil, err
	}

	target, err := ResolveTarget(ctx, DeliveryTarget{
		Channel: in.Channel,
		Target:  in.Target,
	})
	if err != nil {
		return nil, err
	}

	if err := t.router.SendMessage(ctx, target, msg); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"channel":    target.Channel,
		"target":     target.Target,
		"files_sent": len(msg.Files),
	}, nil
}

func buildOutboundMessage(in toolInput) (channel.OutboundMessage, error) {
	text := strings.TrimSpace(in.Text)
	paths := collectPaths(in.File, in.Files, in.Media)
	if text == "" && len(paths) == 0 {
		return channel.OutboundMessage{}, fmt.Errorf(
			"text or files are required",
		)
	}

	msg := channel.OutboundMessage{
		Text:  in.Text,
		Files: make([]channel.OutboundFile, 0, len(paths)),
	}
	for _, path := range paths {
		msg.Files = append(msg.Files, channel.OutboundFile{
			Path: path,
		})
	}
	return msg, nil
}

func collectPaths(groups ...any) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		switch v := group.(type) {
		case string:
			out = appendPath(out, seen, v)
		case []string:
			for _, item := range v {
				out = appendPath(out, seen, item)
			}
		}
	}
	return out
}

func appendPath(
	out []string,
	seen map[string]struct{},
	value string,
) []string {
	path := strings.TrimSpace(value)
	if path == "" {
		return out
	}
	if _, ok := seen[path]; ok {
		return out
	}
	seen[path] = struct{}{}
	return append(out, path)
}

var _ tool.CallableTool = (*Tool)(nil)
