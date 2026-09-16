//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	maxV3RecallContextBytes       = 24 << 10
	maxV3AtomicRecallSectionBytes = 8 << 10
	maxV3CoreRecallSectionBytes   = 8 << 10
)

func latestUserText(req *model.Request) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != model.RoleUser {
			continue
		}
		if text := messageText(msg); text != "" {
			return text
		}
	}
	return ""
}

func injectRecallContext(req *model.Request, rsp *recallResponse) {
	if req == nil || rsp == nil {
		return
	}
	if sys := strings.TrimSpace(firstNonEmpty(
		rsp.AppendSystemContext,
		rsp.Context,
	)); sys != "" {
		prependOrMergeSystem(req, sys)
	}
	if userCtx := strings.TrimSpace(rsp.PrependContext); userCtx != "" {
		insertBeforeLatestUser(req, model.NewUserMessage(userCtx))
	}
}

func formatV3RecallSection(tag, content string, maxBytes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	prefix := "<" + tag + ">\n"
	suffix := "\n</" + tag + ">"
	contentBytes := maxBytes - len(prefix) - len(suffix)
	if contentBytes <= len(v3TruncationMarker) {
		return ""
	}
	content, _ = truncateV3UTF8Bytes(content, contentBytes)
	return prefix + content + suffix
}

func appendV3RecallSystemSection(
	parts []string,
	remaining int,
	tag string,
	content string,
	maxBytes int,
) ([]string, int, bool) {
	separatorBytes := 0
	if len(parts) > 0 {
		separatorBytes = 2
	}
	available := remaining - separatorBytes
	if available <= 0 {
		return parts, remaining, false
	}
	section := formatV3RecallSection(tag, content, min(maxBytes, available))
	if section == "" {
		return parts, remaining, false
	}
	return append(parts, section), remaining - separatorBytes - len(section), true
}

func truncateV3UTF8Bytes(content string, maxBytes int) (string, bool) {
	if len(content) <= maxBytes {
		return content, false
	}
	limit := maxBytes - len(v3TruncationMarker)
	if limit <= 0 {
		return "", true
	}
	end := 0
	for offset := 0; offset < len(content); {
		_, size := utf8.DecodeRuneInString(content[offset:])
		next := offset + size
		if next > limit {
			break
		}
		end = next
		offset = next
	}
	return content[:end] + v3TruncationMarker, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func prependOrMergeSystem(req *model.Request, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	msg := model.NewSystemMessage(text)
	if len(req.Messages) == 0 {
		req.Messages = []model.Message{msg}
		return
	}
	for i := range req.Messages {
		if req.Messages[i].Role != model.RoleSystem {
			continue
		}
		existing := strings.TrimSpace(req.Messages[i].Content)
		if existing == "" {
			req.Messages[i].Content = text
		} else {
			req.Messages[i].Content = existing + "\n\n" + text
		}
		return
	}
	req.Messages = append([]model.Message{msg}, req.Messages...)
}

func insertBeforeLatestUser(req *model.Request, msg model.Message) {
	if req == nil {
		return
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != model.RoleUser {
			continue
		}
		req.Messages = append(req.Messages[:i], append([]model.Message{msg}, req.Messages[i:]...)...)
		return
	}
	req.Messages = append(req.Messages, msg)
}
