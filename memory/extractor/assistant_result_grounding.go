//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var assistantResultMoneyPattern = regexp.MustCompile(
	`(?i)(?:` +
		`(?:[$¥€£]|(?:USD|JPY|EUR|GBP)\b)\s*` +
		`\d[\d,]*(?:\.\d+)?` +
		`(?:\s*(?:[-–—]|\bto\b)\s*` +
		`(?:[$¥€£]|(?:USD|JPY|EUR|GBP)\b)?\s*` +
		`\d[\d,]*(?:\.\d+)?)?` +
		`(?:\s*(?:USD|JPY|EUR|GBP)\b)?` +
		`|` +
		`\d[\d,]*(?:\.\d+)?` +
		`(?:\s*(?:[-–—]|\bto\b)\s*` +
		`\d[\d,]*(?:\.\d+)?)?` +
		`\s*(?:[$¥€£]|(?:USD|JPY|EUR|GBP)\b)` +
		`)`,
)

func filterGroundedAssistantResultOperations(
	ctx context.Context,
	messages []model.Message,
	operations []*Operation,
) []*Operation {
	// Monetary hallucinations are both high-impact and reliably comparable
	// after normalization. Other quantities can be legitimate derivations or
	// formatting changes, so the deterministic guard intentionally stays narrow.
	grounded := moneyClaimSet(assistantResultSourceText(messages))
	result := make([]*Operation, 0, len(operations))
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		claim, ok := firstUngroundedMoneyClaim(operation.Memory, grounded)
		if ok {
			log.DebugfContext(ctx,
				"extractor: dropped assistant result with ungrounded amount %q",
				claim,
			)
			continue
		}
		result = append(result, operation)
	}
	return result
}

func assistantResultSourceText(messages []model.Message) string {
	var source strings.Builder
	for _, message := range messages {
		if message.Role != model.RoleAssistant || message.ToolID != "" ||
			len(message.ToolCalls) > 0 {
			continue
		}
		if text := strings.TrimSpace(message.Content); text != "" {
			source.WriteString(text)
			source.WriteByte('\n')
		}
		for _, part := range message.ContentParts {
			if part.Type != model.ContentTypeText || part.Text == nil {
				continue
			}
			if text := strings.TrimSpace(*part.Text); text != "" {
				source.WriteString(text)
				source.WriteByte('\n')
			}
		}
	}
	return source.String()
}

func moneyClaimSet(text string) map[string]struct{} {
	claims := make(map[string]struct{})
	for _, claim := range assistantResultMoneyPattern.FindAllString(text, -1) {
		claims[normalizeMoneyClaim(claim)] = struct{}{}
	}
	return claims
}

func firstUngroundedMoneyClaim(
	text string,
	grounded map[string]struct{},
) (string, bool) {
	for _, claim := range assistantResultMoneyPattern.FindAllString(text, -1) {
		if _, ok := grounded[normalizeMoneyClaim(claim)]; !ok {
			return claim, true
		}
	}
	return "", false
}

func normalizeMoneyClaim(value string) string {
	value = strings.ToLower(value)
	currency := ""
	for _, candidate := range []struct {
		name   string
		labels []string
	}{
		{name: "usd", labels: []string{"$", "usd"}},
		{name: "jpy", labels: []string{"¥", "jpy"}},
		{name: "eur", labels: []string{"€", "eur"}},
		{name: "gbp", labels: []string{"£", "gbp"}},
	} {
		for _, label := range candidate.labels {
			if strings.Contains(value, label) {
				currency = candidate.name
				break
			}
		}
		if currency != "" {
			break
		}
	}
	value = strings.NewReplacer(
		",", "", " ", "", "\t", "", "\n", "", "\r", "",
		"–", "-", "—", "-", "to", "-",
		"$", "", "usd", "", "¥", "", "jpy", "",
		"€", "", "eur", "", "£", "", "gbp", "",
	).Replace(value)
	return currency + ":" + value
}
