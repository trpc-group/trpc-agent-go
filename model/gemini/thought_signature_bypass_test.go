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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBuildChatConfig_WiresExtrasRequestProvider(t *testing.T) {
	cfg := (&Model{}).buildChatConfig(&model.Request{})
	require.NotNil(t, cfg.HTTPOptions)
	require.NotNil(t, cfg.HTTPOptions.ExtrasRequestProvider)
	body := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{"thoughtSignature": "c2tpcF90aG91Z2h0X3NpZ25hdHVyZV92YWxpZGF0b3I="},
				},
			},
		},
	}
	out := cfg.HTTPOptions.ExtrasRequestProvider(body)
	require.Equal(
		t,
		"skip_thought_signature_validator",
		out["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["thoughtSignature"],
	)
}

func TestRewriteBypassThoughtSignatures_SerializesLiteralSentinel(t *testing.T) {
	contents := []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{{
			FunctionCall: &genai.FunctionCall{
				Name: "execute_series",
				Args: map[string]any{"command": "echo hi"},
			},
			ThoughtSignature: []byte(geminiSkipThoughtSignatureValidator),
		}}, genai.RoleModel),
	}

	parameterMap := make(map[string]any)
	marshaled, err := json.Marshal(map[string]any{
		"model":    "gemini-3-flash-preview",
		"contents": contents,
	})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(marshaled, &parameterMap))

	base64Bypass := base64.StdEncoding.EncodeToString([]byte(geminiSkipThoughtSignatureValidator))
	parts := parameterMap["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	require.Equal(t, base64Bypass, parts[0].(map[string]any)["thoughtSignature"])

	rewriteBypassThoughtSignatures(parameterMap)
	assert.Equal(t, geminiSkipThoughtSignatureValidator, parts[0].(map[string]any)["thoughtSignature"])
}

func TestRewriteBypassThoughtSignatures_PreservesRealSignatures(t *testing.T) {
	realSignature := []byte("real-gemini-signature-bytes")
	parameterMap := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"thoughtSignature": base64.StdEncoding.EncodeToString(realSignature),
					},
				},
			},
		},
	}

	rewriteBypassThoughtSignatures(parameterMap)
	assert.Equal(
		t,
		base64.StdEncoding.EncodeToString(realSignature),
		parameterMap["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["thoughtSignature"],
	)
}

func TestRewriteBypassThoughtSignatures_RewritesByteSliceBypassSentinel(t *testing.T) {
	parameterMap := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"thoughtSignature": []byte(geminiSkipThoughtSignatureValidator),
					},
				},
			},
		},
	}

	rewriteBypassThoughtSignatures(parameterMap)
	assert.Equal(
		t,
		geminiSkipThoughtSignatureValidator,
		parameterMap["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["thoughtSignature"],
	)
}

func TestRewriteBypassThoughtSignatures_RewritesMapSliceContents(t *testing.T) {
	parameterMap := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{
						"thoughtSignature": "c2tpcF90aG91Z2h0X3NpZ25hdHVyZV92YWxpZGF0b3I=",
					},
				},
			},
		},
	}

	rewriteBypassThoughtSignatures(parameterMap)
	assert.Equal(
		t,
		geminiSkipThoughtSignatureValidator,
		parameterMap["contents"].([]map[string]any)[0]["parts"].([]map[string]any)[0]["thoughtSignature"],
	)
}

func TestModel_GenerateContent_SendsLiteralBypassThoughtSignature(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"text": "ok"}]},
				"finishReason": "STOP"
			}]
		}`))
	}))
	t.Cleanup(server.Close)

	ctx := t.Context()
	m, err := New(ctx, "gemini-3-flash-preview", WithGeminiClientConfig(&genai.ClientConfig{
		APIKey:  "test-api-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    server.URL,
			APIVersion: "v1beta",
		},
	}))
	require.NoError(t, err)

	args := []byte(`{"command":"echo hi"}`)
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "run tool"},
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "execute_series",
						Arguments: args,
					},
				}},
			},
			{Role: model.RoleTool, ToolName: "execute_series", Content: `{"output":"hi"}`},
			{Role: model.RoleUser, Content: "continue"},
		},
	}

	respCh, err := m.GenerateContent(ctx, req)
	require.NoError(t, err)
	for range respCh {
	}

	require.NotEmpty(t, capturedBody)
	bodyText := string(capturedBody)
	assert.Contains(t, bodyText, `"thoughtSignature":"skip_thought_signature_validator"`)
	assert.NotContains(t, bodyText, base64.StdEncoding.EncodeToString([]byte(geminiSkipThoughtSignatureValidator)))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &payload))
	bodyStr, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(bodyStr), geminiSkipThoughtSignatureValidator))
}

func TestChainExtrasRequestProvider_RunsBothProviders(t *testing.T) {
	var calls []string
	provider := chainExtrasRequestProvider(
		func(body map[string]any) map[string]any {
			calls = append(calls, "existing")
			body["existing"] = true
			return body
		},
		func(body map[string]any) map[string]any {
			calls = append(calls, "extra")
			body["extra"] = true
			return body
		},
	)

	body := provider(map[string]any{})
	assert.Equal(t, []string{"existing", "extra"}, calls)
	assert.True(t, body["existing"].(bool))
	assert.True(t, body["extra"].(bool))
}
