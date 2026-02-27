//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type displayImageInput struct{}

type displayImageOutput struct {
	Result string `json:"result"`
}

func displayImage(ctx context.Context, _ displayImageInput) (displayImageOutput, error) {
	tc, err := agent.NewToolContext(ctx)
	if err != nil {
		return displayImageOutput{}, fmt.Errorf("failed to create tool context: %w", err)
	}
	value, ok := tc.State[generateImageStateKey]
	if !ok {
		return displayImageOutput{Result: "no image to display"}, nil
	}
	var stateValue generateImageStateValue
	if err := json.Unmarshal(value, &stateValue); err != nil {
		return displayImageOutput{}, fmt.Errorf("failed to unmarshal state: %w", err)
	}
	var output displayImageOutput
	for _, key := range stateValue.ImageIDs {
		desc, err := tc.ResolveArtifact(key, nil)
		if err != nil {
			output.Result += fmt.Sprintf("failed to load image from artifact %s: %s\n", key, err)
			continue
		}
		if desc == nil {
			output.Result += fmt.Sprintf("artifact not found: %s\n", key)
			continue
		}
		output.Result += fmt.Sprintf("Display image MimeType: %s, URL: %s\n", desc.MimeType, desc.URL)
	}
	return output, nil
}

var displayImageTool = function.NewFunctionTool(
	displayImage,
	function.WithName("display-image"),
	function.WithDescription("display image"),
)
