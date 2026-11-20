//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ResultMessage converts a model.ToolResultContent value to a tool message.
//
// The assertion is intentionally exact: other result types remain available to
// the caller's existing JSON handling. The returned message has RoleTool set;
// its caller is responsible for setting ToolID and ToolName.
func ResultMessage(result any) (model.Message, bool) {
	content, ok := result.(model.ToolResultContent)
	if !ok {
		return model.Message{}, false
	}

	message := model.Message{Role: model.RoleTool}
	textOnly := true
	for _, part := range content {
		if part.Type != model.ContentTypeText || part.ContentRef != nil {
			textOnly = false
			break
		}
	}
	if textOnly {
		var text strings.Builder
		for _, part := range content {
			if part.Text != nil {
				text.WriteString(*part.Text)
			}
		}
		message.Content = text.String()
		if message.Content == "" {
			// An explicit empty result must remain distinguishable from the
			// payload-free placeholders used for unfinished tool calls.
			empty := ""
			message.ContentParts = []model.ContentPart{{Type: model.ContentTypeText, Text: &empty}}
		}
		return message, true
	}

	message.ContentParts = []model.ContentPart(content)
	return statecopy.Message(message), true
}
