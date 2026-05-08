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
// It first accepts exact structured outputs of type T or *T. When the runner
// returns a generic JSON-compatible value, it falls back to marshaling that
// payload back into JSON and decoding it into T. When structured output is
// absent, it falls back to decoding the final assistant content as JSON.
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

// decodeStructuredOutput decodes exact typed payloads and generic JSON objects.
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
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal structured output failed: %w", err)
		}
		var decoded T
		if err := json.Unmarshal(payloadJSON, &decoded); err != nil {
			return nil, fmt.Errorf("unmarshal structured output failed: %w", err)
		}
		return &decoded, nil
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
