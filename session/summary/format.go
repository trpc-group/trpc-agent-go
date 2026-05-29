//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	analysisBlockRE = regexp.MustCompile(`(?is)<analysis>.*?</analysis>`)
	summaryBlockRE  = regexp.MustCompile(`(?is)<summary>(.*?)</summary>`)
)

// formatDetailedSummaryOutput strips the optional <analysis>...</analysis>
// scratchpad and unwraps the inner <summary>...</summary> block so that
// DetailedContinuityPrompt-style templates do not leak model thinking into
// persisted summaries. For prompts that do not use these tags, it only trims
// surrounding whitespace.
func formatDetailedSummaryOutput(text string) string {
	text = analysisBlockRE.ReplaceAllString(text, "")
	if match := summaryBlockRE.FindStringSubmatch(text); len(match) == 2 {
		text = match[1]
	}
	return strings.TrimSpace(text)
}

func messageContentForSummary(msg model.Message) string {
	parts := make([]string, 0, 1+len(msg.ContentParts))
	if strings.TrimSpace(msg.Content) != "" {
		parts = append(parts, msg.Content)
	}
	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && strings.TrimSpace(*part.Text) != "" {
				parts = append(parts, *part.Text)
			}
		case model.ContentTypeImage:
			parts = append(parts, "[image attachment]")
		case model.ContentTypeAudio:
			parts = append(parts, "[audio attachment]")
		case model.ContentTypeFile:
			parts = append(parts, filePartSummary(part.File))
		}
	}
	return strings.Join(parts, "\n")
}

func filePartSummary(file *model.File) string {
	if file == nil {
		return "[file attachment]"
	}
	name := strings.TrimSpace(file.Name)
	if name != "" {
		return fmt.Sprintf("[file attachment: %s]", name)
	}
	if fileURL := strings.TrimSpace(file.URL); fileURL != "" {
		return fmt.Sprintf("[file attachment: %s]", fileURL)
	}
	if fileID := strings.TrimSpace(file.FileID); fileID != "" {
		return fmt.Sprintf("[file attachment: %s]", fileID)
	}
	return "[file attachment]"
}
