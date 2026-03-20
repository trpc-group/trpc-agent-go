//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	toolResultMediaLineFile = "MEDIA:"
	toolResultMediaLineDir  = "MEDIA_DIR:"

	maxToolResultImages = 6

	maxToolResultImageBytes int64 = 12 << 20

	toolResultImagesTraceKind = "tool.result.images"
)

const (
	toolResultSingleImageText = "Generated image attached for " +
		"direct inspection: %s."
	toolResultMultiImageText = "Generated images attached for " +
		"direct inspection: %s."
)

type toolResultMediaPayload struct {
	Output     string   `json:"output,omitempty"`
	MediaFiles []string `json:"media_files,omitempty"`
	MediaDirs  []string `json:"media_dirs,omitempty"`
}

type toolResultImage struct {
	Name   string
	Data   []byte
	Format string
}

func openClawToolResultMessages(
	ctx context.Context,
	in *tool.ToolResultMessagesInput,
) (any, error) {
	out, err := toolResultImageMessages(ctx, in)
	if out != nil || err != nil {
		return out, err
	}
	return mcpImageResultMessages(ctx, in)
}

func toolResultImageMessages(
	ctx context.Context,
	in *tool.ToolResultMessagesInput,
) (any, error) {
	if in == nil {
		return nil, nil
	}

	defaultMsg, ok := in.DefaultToolMessage.(model.Message)
	if !ok {
		return nil, nil
	}

	images := loadToolResultImages(in.Result)
	if len(images) == 0 {
		return nil, nil
	}

	recordToolResultImages(ctx, in.ToolName, images)

	userMsg := model.Message{
		Role:    model.RoleUser,
		Content: toolResultImageMessageText(images),
	}
	for _, img := range images {
		userMsg.AddImageData(
			img.Data,
			mcpImageDetailAuto,
			img.Format,
		)
	}

	return []model.Message{defaultMsg, userMsg}, nil
}

func loadToolResultImages(result any) []toolResultImage {
	paths := collectToolResultImagePaths(result)
	if len(paths) == 0 {
		return nil
	}

	images := make([]toolResultImage, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > maxToolResultImageBytes {
			continue
		}

		format, ok := toolResultImageFormat(path)
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		images = append(images, toolResultImage{
			Name: uploads.PreferredName(
				filepath.Base(path),
				"",
			),
			Data:   data,
			Format: format,
		})
		if len(images) >= maxToolResultImages {
			break
		}
	}

	if len(images) == 0 {
		return nil
	}
	return images
}

func collectToolResultImagePaths(result any) []string {
	payload, ok := parseToolResultMediaPayload(result)
	if !ok {
		return nil
	}

	out := make([]string, 0, maxToolResultImages)
	seen := make(map[string]struct{})
	for _, raw := range payload.MediaFiles {
		out = appendToolResultImagePath(out, seen, raw)
		if len(out) >= maxToolResultImages {
			return out
		}
	}
	for _, raw := range payload.MediaDirs {
		out = appendToolResultImagePath(out, seen, raw)
		if len(out) >= maxToolResultImages {
			return out
		}
	}
	for _, raw := range toolResultOutputPaths(payload.Output) {
		out = appendToolResultImagePath(out, seen, raw)
		if len(out) >= maxToolResultImages {
			return out
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseToolResultMediaPayload(
	result any,
) (toolResultMediaPayload, bool) {
	if result == nil {
		return toolResultMediaPayload{}, false
	}

	body, err := json.Marshal(result)
	if err != nil {
		return toolResultMediaPayload{}, false
	}

	var payload toolResultMediaPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return toolResultMediaPayload{}, false
	}
	return payload, true
}

func appendToolResultImagePath(
	out []string,
	seen map[string]struct{},
	raw string,
) []string {
	path, ok := resolveToolResultPath(raw)
	if !ok {
		return out
	}

	info, err := os.Stat(path)
	if err != nil {
		return out
	}
	if !info.IsDir() {
		if _, ok := toolResultImageFormat(path); !ok {
			return out
		}
		return appendUniqueToolResultPath(out, seen, path)
	}

	return appendToolResultDirImages(out, seen, path)
}

func appendToolResultDirImages(
	out []string,
	seen map[string]struct{},
	root string,
) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if len(out) >= maxToolResultImages {
			return out
		}
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			out = appendToolResultDirImages(out, seen, path)
			continue
		}
		if _, ok := toolResultImageFormat(path); !ok {
			continue
		}
		out = appendUniqueToolResultPath(out, seen, path)
	}
	return out
}

func appendUniqueToolResultPath(
	out []string,
	seen map[string]struct{},
	path string,
) []string {
	clean := filepath.Clean(path)
	if _, ok := seen[clean]; ok {
		return out
	}
	seen[clean] = struct{}{}
	return append(out, clean)
}

func toolResultOutputPaths(output string) []string {
	if strings.TrimSpace(output) == "" {
		return nil
	}

	out := make([]string, 0, 2)
	for _, line := range strings.Split(output, "\n") {
		path, ok := toolResultPathFromLine(line)
		if !ok {
			continue
		}
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toolResultPathFromLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}

	switch {
	case strings.HasPrefix(trimmed, toolResultMediaLineDir):
		trimmed = strings.TrimSpace(
			strings.TrimPrefix(trimmed, toolResultMediaLineDir),
		)
	case strings.HasPrefix(trimmed, toolResultMediaLineFile):
		trimmed = strings.TrimSpace(
			strings.TrimPrefix(trimmed, toolResultMediaLineFile),
		)
	default:
		return "", false
	}

	return resolveToolResultPath(trimmed)
}

func resolveToolResultPath(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.Trim(trimmed, "`\"'")
	if trimmed == "" {
		return "", false
	}
	if path, ok := uploads.PathFromHostRef(trimmed); ok {
		return filepath.Clean(path), true
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), true
	}
	return "", false
}

func toolResultImageFormat(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png":
		return "png", true
	case ".jpg":
		return "jpg", true
	case ".jpeg":
		return "jpeg", true
	case ".webp":
		return "webp", true
	case ".gif":
		return "gif", true
	default:
		return "", false
	}
}

func toolResultImageMessageText(images []toolResultImage) string {
	names := make([]string, 0, len(images))
	for _, img := range images {
		name := strings.TrimSpace(img.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	list := strings.Join(names, ", ")
	if list == "" {
		if len(images) == 1 {
			return "Generated image attached for direct inspection."
		}
		return "Generated images attached for direct inspection."
	}
	if len(images) == 1 {
		return fmt.Sprintf(toolResultSingleImageText, list)
	}
	return fmt.Sprintf(toolResultMultiImageText, list)
}

func recordToolResultImages(
	ctx context.Context,
	toolName string,
	images []toolResultImage,
) {
	trace := debugrecorder.TraceFromContext(ctx)
	if trace == nil || len(images) == 0 {
		return
	}

	names := make([]string, 0, len(images))
	for _, img := range images {
		if name := strings.TrimSpace(img.Name); name != "" {
			names = append(names, name)
		}
	}
	_ = trace.Record(toolResultImagesTraceKind, map[string]any{
		"tool_name": toolName,
		"images":    names,
		"count":     len(images),
	})
}
