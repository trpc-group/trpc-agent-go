//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package responsejson decodes structured JSON judge responses.
package responsejson

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonutils"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// UnmarshalContent decodes the first response choice content as JSON into dst.
// Leading prose, markdown fences, trailing footers, and minor JSON syntax errors
// from judge models are repaired via internal/jsonutils before unmarshaling.
func UnmarshalContent(resp *model.Response, dst any) error {
	if resp == nil {
		return fmt.Errorf("response is nil")
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return fmt.Errorf("empty response text")
	}
	if err := jsonutils.DecodeFlexibleJSON(content, dst); err != nil {
		return fmt.Errorf("unmarshal response json: %w", err)
	}
	return nil
}
