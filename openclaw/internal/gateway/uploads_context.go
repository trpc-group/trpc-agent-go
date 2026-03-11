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

	recentUploadContextHeader = "Recent chat files/media available " +
		"to tools in this session (newest first):"
	recentUploadKindHeader = "Latest matching file by kind " +
		"in this chat:"
	recentUploadContextFooter = "Use OPENCLAW_LAST_UPLOAD_* " +
		"for the newest file. For multiple recent files in " +
		"exec_command, use OPENCLAW_RECENT_UPLOADS_JSON. " +
		"For common file-reading tasks, prefer read_document " +
		"or read_spreadsheet before exec_command. If a single " +
		"matching upload is already present, call those tools " +
		"directly instead of printing OPENCLAW_* variables. " +
		"When calling message to send an existing upload back " +
		"to the user, prefer the stable host_ref from that " +
		"JSON or OPENCLAW_LAST_UPLOAD_HOST_REF instead of " +
		"guessing a local path. " +
		"This list may include both user uploads and bot-" +
		"generated files previously sent in this chat. " +
		"When the user says 'the PDF/audio/video I just " +
		"sent' or 'the file you just sent me', resolve " +
		"against this list first. If the " +
		"target file came from a recent bot reply, it is " +
		"still valid to reuse it. If the " +
		"user replies to an earlier media message, that " +
		"replied media is usually the intended target. If the " +
		"agent wants OpenClaw chat channels to auto-attach " +
		"generated outputs, it may include final reply lines like " +
		"`MEDIA: /path/to/file` or `MEDIA_DIR: /path/to/dir`; " +
		"those directive lines are hidden from users while the " +
		"matching files are sent. To send compatible audio as a " +
		"Telegram voice bubble, it may also include " +
		"`[[audio_as_voice]]` in the final reply. " +
		"Avoid repeating local machine " +
		"paths or placeholder Telegram filenames " +
		"in user-facing replies unless the user explicitly asks. " +
		"If the " +
		"requested media kind is not present here, say " +
		"which files are currently available in this chat."
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
		kind := describeUploadKind(file.Name, file.MimeType)
		if kind == "" || kind == uploadKindFileLabel {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		name := uploadContextName(file)
		if name == "" {
			continue
		}
		parts = append(parts, "- "+kind+": "+name)
	}
	return strings.Join(parts, "\n")
}

const uploadKindFileLabel = "file"

func formatUploadContextLine(file uploads.ListedFile) string {
	name := uploadContextName(file)
	kind := describeUploadKind(name, file.MimeType)
	source := describeUploadSource(file.Source)
	if kind == "" && source == "" {
		return "- " + name
	}
	switch {
	case kind != "" && source != "":
		return "- " + name + " [" + kind + ", " + source + "]"
	case kind != "":
		return "- " + name + " [" + kind + "]"
	default:
		return "- " + name + " [" + source + "]"
	}
}

func uploadContextName(file uploads.ListedFile) string {
	name := strings.TrimSpace(file.Name)
	if name == "" {
		name = filepath.Base(strings.TrimSpace(file.Path))
	}
	name = uploads.PreferredName(name, file.MimeType)
	if !isUploadPlaceholderName(file.Name) ||
		strings.TrimSpace(file.Source) != uploads.SourceInbound {
		return name
	}

	switch uploads.KindFromMeta(file.Name, file.MimeType) {
	case uploads.KindAudio:
		return "your audio message"
	case uploads.KindVideo:
		return "your video"
	case uploads.KindImage:
		return "your image"
	case uploads.KindPDF:
		return "your PDF"
	default:
		return "your file"
	}
}

func isUploadPlaceholderName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	stem := trimmed
	if dot := strings.Index(trimmed, "."); dot >= 0 {
		stem = trimmed[:dot]
	}
	if !strings.HasPrefix(stem, "file_") {
		return false
	}
	suffix := strings.TrimPrefix(stem, "file_")
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func describeUploadKind(name string, mimeType string) string {
	return uploads.KindFromMeta(name, mimeType)
}

func describeUploadSource(source string) string {
	switch strings.TrimSpace(source) {
	case uploads.SourceDerived:
		return "derived"
	default:
		return ""
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
