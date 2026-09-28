//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"strings"

	utilmessage "trpc.group/trpc-go/trpc-agent-go/internal/util/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// retainTypedUserMessage applies user_input to a last user message when the
// current invocation is ContentParts-only. The invocation TextContent is the
// baseline: unchanged input keeps the typed message (including processor
// annotations and any previously merged Content); a rewrite replaces the
// matching original text-index window and leaves non-text parts in place. If
// a message with non-text parts cannot be matched, it is preserved instead of
// collapsing to NewUserMessage.
func retainTypedUserMessage(
	state State,
	last model.Message,
	userInput string,
) (model.Message, bool) {
	if userInput == "" || len(last.ContentParts) == 0 ||
		!invocationIsContentPartsOnly(state) {
		return model.Message{}, false
	}
	original := invocationTextContent(state)
	if original != "" && userInput == original {
		return last, true
	}
	if original != "" && userInput != original {
		if rewritten, ok := rewriteContentPartsUserText(last, original, userInput); ok {
			return rewritten, true
		}
	}
	if hasNonTextContentPart(last) {
		return last, true
	}
	return model.Message{}, false
}

func invocationIsContentPartsOnly(state State) bool {
	execCtx := executionContextFromState(state)
	if execCtx == nil || execCtx.Invocation == nil {
		return false
	}
	msg := execCtx.Invocation.Message
	return msg.Content == "" && len(msg.ContentParts) > 0
}

func invocationTextContent(state State) string {
	execCtx := executionContextFromState(state)
	if execCtx == nil || execCtx.Invocation == nil {
		return ""
	}
	return utilmessage.TextContent(execCtx.Invocation.Message)
}

func hasNonTextContentPart(msg model.Message) bool {
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText {
			return true
		}
	}
	return false
}

func rewriteContentPartsUserText(
	msg model.Message,
	original string,
	userInput string,
) (model.Message, bool) {
	if original == "" || userInput == original || len(msg.ContentParts) == 0 {
		return model.Message{}, false
	}
	parts := append([]model.ContentPart(nil), msg.ContentParts...)
	indexes := make([]int, 0, len(parts))
	for i, part := range parts {
		if nonEmptyTextPart(part) {
			indexes = append(indexes, i)
		}
	}
	for start := 0; start < len(indexes); start++ {
		joined := make([]string, 0, len(indexes)-start)
		for end := start; end < len(indexes); end++ {
			joined = append(joined, *parts[indexes[end]].Text)
			if strings.Join(joined, "\n") != original {
				continue
			}
			text := userInput
			drop := make(map[int]struct{}, end-start)
			for _, idx := range indexes[start+1 : end+1] {
				drop[idx] = struct{}{}
			}
			kept := make([]model.ContentPart, 0, len(parts)-(end-start))
			for i, part := range parts {
				if _, ok := drop[i]; ok {
					continue
				}
				if i == indexes[start] {
					kept = append(kept, model.ContentPart{
						Type: model.ContentTypeText,
						Text: &text,
					})
					continue
				}
				kept = append(kept, part)
			}
			out := msg
			out.ContentParts = kept
			return out, true
		}
	}
	return model.Message{}, false
}

func nonEmptyTextPart(part model.ContentPart) bool {
	return part.Type == model.ContentTypeText && part.Text != nil && *part.Text != ""
}
