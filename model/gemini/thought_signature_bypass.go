//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"encoding/base64"

	"google.golang.org/genai"
)

// geminiContextEngineeringBypass is Google's alternate documented bypass token for
// injected function calls. See:
// https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures
const geminiContextEngineeringBypass = "context_engineering_is_the_way_to_go"

var geminiThoughtSignatureBypassSentinels = []string{
	geminiSkipThoughtSignatureValidator,
	geminiContextEngineeringBypass,
}

// chainExtrasRequestProvider composes two genai request-body mutators so user-provided
// providers from WithGeminiClientConfig continue to run before our rewriter.
func chainExtrasRequestProvider(existing, extra genai.ExtrasRequestProvider) genai.ExtrasRequestProvider {
	if extra == nil {
		return existing
	}
	if existing == nil {
		return extra
	}
	return func(body map[string]any) map[string]any {
		body = existing(body)
		return extra(body)
	}
}

// rewriteBypassThoughtSignatures replaces base64-encoded bypass sentinel bytes with
// the literal strings required by the Gemini API. go-genai stores ThoughtSignature as
// []byte, which InternalDeepMarshal JSON-encodes as base64 on the wire.
func rewriteBypassThoughtSignatures(body map[string]any) map[string]any {
	if body == nil {
		return body
	}
	rewriteThoughtSignaturesInContents(body)
	return body
}

func rewriteThoughtSignaturesInContents(body map[string]any) {
	forEachContentMap(body["contents"], rewriteThoughtSignaturesInContent)
}

func forEachContentMap(raw any, fn func(map[string]any)) {
	switch contents := raw.(type) {
	case []map[string]any:
		for _, content := range contents {
			fn(content)
		}
	case []any:
		for _, item := range contents {
			content, ok := item.(map[string]any)
			if !ok {
				continue
			}
			fn(content)
		}
	}
}

func rewriteThoughtSignaturesInContent(content map[string]any) {
	forEachPartMap(content["parts"], rewriteBypassThoughtSignatureField)
}

func forEachPartMap(raw any, fn func(map[string]any)) {
	switch parts := raw.(type) {
	case []map[string]any:
		for _, part := range parts {
			fn(part)
		}
	case []any:
		for _, item := range parts {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			fn(part)
		}
	}
}

func rewriteBypassThoughtSignatureField(part map[string]any) {
	raw, ok := part["thoughtSignature"]
	if !ok {
		return
	}
	switch v := raw.(type) {
	case string:
		if sentinel, matched := bypassSentinelForWireValue(v); matched {
			part["thoughtSignature"] = sentinel
		}
	case []byte:
		if sentinel, matched := bypassSentinelForWireValue(string(v)); matched {
			part["thoughtSignature"] = sentinel
			return
		}
		if sentinel, matched := bypassSentinelForWireValue(base64.StdEncoding.EncodeToString(v)); matched {
			part["thoughtSignature"] = sentinel
		}
	}
}

func bypassSentinelForWireValue(wire string) (string, bool) {
	for _, sentinel := range geminiThoughtSignatureBypassSentinels {
		if wire == sentinel {
			return sentinel, true
		}
		if wire == base64.StdEncoding.EncodeToString([]byte(sentinel)) {
			return sentinel, true
		}
	}
	return "", false
}
