//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	recentUploadContextLimit = 6

	recentUploadContextHeader = "Recent chat uploads available " +
		"to tools in this session (newest first):"
	recentUploadKindHeader = "Latest matching upload by kind " +
		"in this chat:"
	recentUploadContextFooter = "Use OPENCLAW_LAST_UPLOAD_* " +
		"for the newest upload. For multiple uploads in " +
		"exec_command, use OPENCLAW_RECENT_UPLOADS_JSON. " +
		"When the user says 'the PDF/audio/video I just " +
		"sent', resolve against this list first. If the " +
		"user replies to an earlier media message, that " +
		"replied media is usually the intended target. If the " +
		"requested media kind is not present here, say " +
		"which uploads are currently available in this chat."
)

func (s *Server) uploadContextMessages(
	userID string,
	sessionID string,
) []model.Message {
	if s == nil || s.uploads == nil {
		return nil
	}

	channel := channelFromSessionID(sessionID)
	files, err := s.uploads.ListScope(
		uploads.Scope{
			Channel:   channel,
			UserID:    userID,
			SessionID: sessionID,
		},
		recentUploadContextLimit,
	)
	if err != nil || len(files) == 0 {
		return nil
	}
	return []model.Message{
		model.NewSystemMessage(buildUploadContextText(files)),
	}
}

func buildUploadContextText(files []uploads.ListedFile) string {
	if len(files) == 0 {
		return ""
	}

	lines := make([]string, 0, len(files)+2)
	lines = append(lines, recentUploadContextHeader)
	for _, file := range files {
		lines = append(lines, formatUploadContextLine(file))
	}
	if summary := buildUploadKindSummary(files); summary != "" {
		lines = append(lines, recentUploadKindHeader)
		lines = append(lines, summary)
	}
	lines = append(lines, recentUploadContextFooter)
	return strings.Join(lines, "\n")
}

func buildUploadKindSummary(files []uploads.ListedFile) string {
	if len(files) == 0 {
		return ""
	}

	seen := make(map[string]struct{})
	parts := make([]string, 0, 4)
	for _, file := range files {
		kind := describeUploadKind(file.Name)
		if kind == "" || kind == uploadKindFileLabel {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		name := strings.TrimSpace(file.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(file.Path))
		}
		if name == "" {
			continue
		}
		parts = append(parts, "- "+kind+": "+name)
	}
	return strings.Join(parts, "\n")
}

const uploadKindFileLabel = "file"

func formatUploadContextLine(file uploads.ListedFile) string {
	name := strings.TrimSpace(file.Name)
	if name == "" {
		name = filepath.Base(strings.TrimSpace(file.Path))
	}
	kind := describeUploadKind(name)
	if kind == "" {
		return "- " + name
	}
	return "- " + name + " [" + kind + "]"
}

func describeUploadKind(name string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "image"
	case ".mp3", ".wav", ".ogg", ".oga", ".m4a":
		return "audio"
	case ".mp4", ".mov", ".webm", ".mkv":
		return "video"
	case ".pdf":
		return "pdf"
	default:
		return uploadKindFileLabel
	}
}

func channelFromSessionID(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return defaultChannelName
	}
	if idx := strings.Index(trimmed, ":"); idx > 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return defaultChannelName
}
