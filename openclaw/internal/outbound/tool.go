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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolMessage = "message"

	maxExpandedFileCount = 32
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
						"user. Existing directories are expanded " +
						"to their files, and host globs are " +
						"expanded when they match. Telegram " +
						"auto-picks document, photo, audio, " +
						"voice, or video upload mode based on " +
						"the file type.",
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
				"as_voice": {
					Type: "boolean",
					Description: "When true, send compatible " +
						"audio files as voice notes on channels " +
						"that support them.",
				},
				"audio_as_voice": {
					Type:        "boolean",
					Description: "Alias for as_voice.",
				},
			},
		},
	}
}

type toolInput struct {
	Text         string   `json:"text"`
	File         string   `json:"file,omitempty"`
	Files        []string `json:"files,omitempty"`
	Media        []string `json:"media,omitempty"`
	Channel      string   `json:"channel,omitempty"`
	Target       string   `json:"target,omitempty"`
	AsVoice      bool     `json:"as_voice,omitempty"`
	AudioAsVoice bool     `json:"audio_as_voice,omitempty"`
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
	files, err := expandOutboundFiles(paths)
	if err != nil {
		return channel.OutboundMessage{}, err
	}
	asVoice := in.AsVoice || in.AudioAsVoice
	for _, path := range files {
		msg.Files = append(msg.Files, channel.OutboundFile{
			Path:    path,
			AsVoice: asVoice,
		})
	}
	return msg, nil
}

func expandOutboundFiles(paths []string) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		expanded, err := expandOutboundPath(raw)
		if err != nil {
			return nil, err
		}
		if len(expanded) == 0 {
			out = appendPath(out, seen, raw)
			continue
		}
		for _, item := range expanded {
			out = appendPath(out, seen, item)
		}
	}
	return out, nil
}

func expandOutboundPath(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if isOpaqueRef(trimmed) {
		return nil, nil
	}
	if matches, err := expandOutboundGlob(trimmed); err != nil {
		return nil, err
	} else if len(matches) > 0 {
		return matches, nil
	}
	return expandOutboundDirectory(trimmed)
}

func isOpaqueRef(raw string) bool {
	if strings.HasPrefix(raw, "host://") {
		return false
	}
	if strings.HasPrefix(raw, "file://") {
		return false
	}
	return strings.Contains(raw, "://")
}

func expandOutboundGlob(raw string) ([]string, error) {
	if !strings.ContainsAny(raw, "*?[") {
		return nil, nil
	}
	matches, err := filepath.Glob(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid file glob: %w", err)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Strings(matches)
	return absPaths(matches), nil
}

func expandOutboundDirectory(raw string) ([]string, error) {
	path := strings.TrimSpace(raw)
	if resolved, ok := uploads.PathFromHostRef(path); ok {
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve dir: %w", err)
	}
	files := make([]string, 0, 4)
	err = filepath.WalkDir(
		root,
		func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d == nil || d.IsDir() {
				return nil
			}
			files = append(files, path)
			if len(files) >= maxExpandedFileCount {
				return fs.SkipAll
			}
			return nil
		},
	)
	if err != nil && err != fs.SkipAll {
		return nil, fmt.Errorf("expand dir: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func absPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved := strings.TrimSpace(path)
		if resolved == "" {
			continue
		}
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		out = append(out, resolved)
	}
	return out
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
