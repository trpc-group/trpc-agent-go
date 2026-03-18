//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package decode provides typed output decoders for PromptIter internals.
package decode

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	irunner "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/runner"
)

// DecodeOutputJSON decodes one runner output into one typed JSON payload.
//
// It accepts only exact structured outputs of type T or *T. When structured
// output is absent, it falls back to decoding the final assistant content as
// JSON.
func DecodeOutputJSON[T any](output *irunner.Output) (*T, error) {
	if output == nil {
		return nil, nil
	}
	if output.StructuredOutput != nil {
		decoded, err := decodeStructuredOutputExact[T](output.StructuredOutput)
		if err != nil {
			return nil, fmt.Errorf("parse structured output: %w", err)
		}
		if decoded != nil {
			return decoded, nil
		}
	}
	decoded, err := decodeFinalContentJSON[T](output.FinalContent)
	if err != nil {
		return nil, fmt.Errorf("parse final content: %w", err)
	}
	return decoded, nil
}

// decodeStructuredOutputExact accepts only exact typed structured outputs.
func decodeStructuredOutputExact[T any](payload any) (*T, error) {
	if payload == nil {
		return nil, nil
	}
	targetType := reflect.TypeOf((*T)(nil)).Elem()
	payloadValue := reflect.ValueOf(payload)
	payloadType := payloadValue.Type()

	switch {
	case payloadType == targetType:
		decoded := new(T)
		reflect.ValueOf(decoded).Elem().Set(payloadValue)
		return decoded, nil
	case payloadType == reflect.PointerTo(targetType):
		if payloadValue.IsNil() {
			return nil, nil
		}
		decoded := new(T)
		reflect.ValueOf(decoded).Elem().Set(payloadValue.Elem())
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported structured output type %T", payload)
	}
}

// decodeFinalContentJSON decodes the final assistant content as JSON.
func decodeFinalContentJSON[T any](content string) (*T, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, nil
	}
	var decoded T
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, fmt.Errorf("unmarshal output JSON failed: %w", err)
	}
	return &decoded, nil
}
