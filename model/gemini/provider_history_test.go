//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func responsesHistoryMessage(payload, text string, calls ...model.ToolCall) model.Message {
	return model.Message{
		Role: model.RoleAssistant, Content: text, ToolCalls: calls,
		ReasoningContent: "private-summary", ReasoningSignature: "private-signature",
		ProviderData: &model.ProviderData{Protocol: "openai.responses", Model: "old-model", Payload: json.RawMessage(payload)},
	}
}

func historyCall(id, name, args string) model.ToolCall {
	return model.ToolCall{ID: id, Type: "function", Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(args)}}
}

func TestSupportsResponsesProviderHistory(t *testing.T) {
	m := &Model{}
	require.True(t, m.SupportsProviderHistory("openai.responses"))
	require.False(t, m.SupportsProviderHistory(""))
	require.False(t, m.SupportsProviderHistory("other"))
}

type capturedHistoryRequest struct {
	body []byte
	err  error
}

func newHistoryServer(t *testing.T) (*httptest.Server, <-chan capturedHistoryRequest) {
	t.Helper()
	requests := make(chan capturedHistoryRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		select {
		case requests <- capturedHistoryRequest{body: body, err: err}:
		default:
			t.Error("unexpected extra Gemini request")
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, "could not read request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestResponsesHistoryWirePreservesParallelCallsAndNumbers(t *testing.T) {
	server, requests := newHistoryServer(t)

	args := `{"number":9007199254740992,"nested":[100000000000000000000]}`
	payload := fmt.Sprintf(`[{"type":"reasoning","id":"rs_1","encrypted_content":"private-encrypted"},{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"private-original"}]},{"type":"function_call","call_id":"c1","name":"old","arguments":%q},{"type":"function_call","call_id":"c2","name":"old","arguments":"{}"}]`, args)
	native := responsesHistoryMessage(payload, "visible-edit", historyCall("c1", "lookup", args), historyCall("c2", "lookup", `{}`))
	native.ToolCalls[0].ExtraFields = map[string]any{"private": "private-extra"}
	signed := historyCall("g1", "legacy", `{}`)
	signed.ExtraFields = map[string]any{geminiThoughtSignatureKey: []byte("real-gemini-signature")}
	messages := []model.Message{
		model.NewUserMessage("question"),
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{signed}},
		model.NewToolMessage("g1", "legacy", "previous"),
		native,
		model.NewToolMessage("c2", "lookup", `{"answer":9007199254740994}`),
		model.NewToolMessage("c1", "lookup", `{"answer":9007199254740993,"rate":0.1234567890123456789}`),
		model.NewUserMessage("continue"),
	}
	messages[0].ContentParts = []model.ContentPart{{
		Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("image bytes"), Format: "image/png"},
	}}
	before, err := json.Marshal(messages)
	require.NoError(t, err)
	var callbackRequest []*genai.Content
	m, err := New(context.Background(), "gemini-test", WithGeminiClientConfig(&genai.ClientConfig{
		APIKey: "test-key", Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, APIVersion: "v1beta"},
	}), WithChatRequestCallback(func(_ context.Context, contents []*genai.Content) {
		callbackRequest = contents
		contents[0].Parts[1].InlineData.Data[0] = 'I'
		contents[3].Parts[1].FunctionCall.Args["callback_edit"] = "retained"
	}))
	require.NoError(t, err)
	request := &model.Request{Messages: messages}
	stream, err := m.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	for response := range stream {
		require.Nil(t, response.Error)
	}
	require.Len(t, requests, 1)
	captured := <-requests
	require.NoError(t, captured.err)
	wire := captured.body
	require.Contains(t, string(wire), `"number":9007199254740992`)
	require.Contains(t, string(wire), `"answer":9007199254740994`)
	require.Contains(t, string(wire), `"nested":[100000000000000000000]`)
	require.Contains(t, string(wire), `"callback_edit":"retained"`)
	require.Contains(t, string(wire), `"thoughtSignature":"skip_thought_signature_validator"`)
	for _, secret := range []string{"private-summary", "private-signature", "private-encrypted", "private-original", "private-extra"} {
		require.NotContains(t, string(wire), secret)
	}
	require.Contains(t, string(wire), "visible-edit")
	var body struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				FunctionCall *struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"functionCall"`
				FunctionResponse *struct {
					ID       string          `json:"id"`
					Name     string          `json:"name"`
					Response json.RawMessage `json:"response"`
				} `json:"functionResponse"`
				ThoughtSignature string `json:"thoughtSignature"`
			} `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(wire, &body))
	require.Len(t, body.Contents, 6)
	require.Equal(t, "model", body.Contents[1].Role)
	require.Equal(t, "cmVhbC1nZW1pbmktc2lnbmF0dXJl", body.Contents[1].Parts[0].ThoughtSignature)
	require.Len(t, body.Contents[4].Parts, 2)
	require.Equal(t, "user", body.Contents[4].Role)
	require.Equal(t, "c1", body.Contents[4].Parts[0].FunctionResponse.ID)
	require.Equal(t, "c2", body.Contents[4].Parts[1].FunctionResponse.ID)
	require.Equal(t, "lookup", body.Contents[4].Parts[0].FunctionResponse.Name)
	require.JSONEq(t, `{"output":"{\"answer\":9007199254740993,\"rate\":0.1234567890123456789}"}`, string(body.Contents[4].Parts[0].FunctionResponse.Response))
	require.Equal(t, json.Number("9007199254740992"), callbackRequest[3].Parts[1].FunctionCall.Args["number"])
	after, err := json.Marshal(request.Messages)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

func TestResponsesHistoryEmptyPartPreservesVisibleCitations(t *testing.T) {
	const payload = `[{"type":"message","id":"m1","status":"completed","role":"assistant","content":[
  {"type":"output_text","text":"Original.","annotations":[{"type":"url_citation","url":"https://example.test/source"}]}]}]`
	for _, tc := range []struct{ text, want string }{
		{"Original.", "Original.\n\nSources:\nhttps://example.test/source"},
		{"Redacted.", "Redacted."},
	} {
		t.Run(tc.text, func(t *testing.T) {
			message := responsesHistoryMessage(payload, tc.text)
			message.ContentParts = []model.ContentPart{{}}
			prepared, _, err := (&Model{}).prepareProviderHistory([]model.Message{message})
			require.NoError(t, err)
			require.Len(t, prepared, 1)
			require.Equal(t, tc.want, prepared[0].Content)
			require.Empty(t, prepared[0].ContentParts)
			require.Len(t, message.ContentParts, 1)
		})
	}
}

func TestResponsesHistoryRejectsIncompleteOrUnrepresentableInput(t *testing.T) {
	native := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
	other := responsesHistoryMessage(`[]`, "visible")
	wrongProtocol := other
	wrongProtocol.ProviderData = &model.ProviderData{Protocol: "other", Model: "old", Payload: json.RawMessage(`[]`)}
	wrongID := native
	wrongID.ToolCalls = []model.ToolCall{historyCall("wrong", "lookup", `{}`)}
	invalidArgs := native
	invalidArgs.ToolCalls = []model.ToolCall{historyCall("c1", "lookup", `[]`)}
	duplicate := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{historyCall("c1", "lookup", `{}`), historyCall("c1", "lookup", `{}`)}}
	image := model.NewToolMessage("c1", "lookup", "result")
	image.AddImageURL("https://example.test/image.png", "")
	fileID := model.NewUserMessage("read")
	fileID.AddFileID("file-123")
	ref := model.NewToolMessage("c1", "lookup", "result")
	ref.ContentParts = []model.ContentPart{{ContentRef: &model.ContentRef{ArtifactRef: "artifact://private"}}}
	tests := []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{"hosted search without output", []model.Message{responsesHistoryMessage(`[{"type":"tool_search_call","execution":"server","arguments":{}}]`, "")}, "has no tool_search_output"},
		{"client search at end", []model.Message{responsesHistoryMessage(`[{"type":"tool_search_call","execution":"client","call_id":"s","arguments":{}}]`, "")}, "has no tool_search_output"},
		{"client search before next turn", []model.Message{responsesHistoryMessage(`[{"type":"tool_search_call","execution":"client","call_id":"s","arguments":{}}]`, ""), model.NewUserMessage("continue")}, "has no tool_search_output"},
		{"protocol", []model.Message{wrongProtocol}, "provider data"},
		{"mismatched call id", []model.Message{wrongID}, "does not match"},
		{"incomplete native", []model.Message{responsesHistoryMessage(`[{"type":"function_call","status":"in_progress","call_id":"c1","name":"lookup","arguments":"{}"}]`, "")}, "in_progress"},
		{"invalid call arguments", []model.Message{invalidArgs}, "arguments"},
		{"missing result", []model.Message{native, model.NewUserMessage("next")}, "missing tool results"},
		{"orphan result", []model.Message{other, model.NewToolMessage("unknown", "lookup", "x")}, "no pending"},
		{"duplicate result", []model.Message{native, model.NewToolMessage("c1", "lookup", "x"), model.NewToolMessage("c1", "lookup", "y")}, "no pending"},
		{"duplicate call", []model.Message{other, duplicate}, "duplicate function call"},
		{"media in tool result", []model.Message{native, image}, "tool result content type"},
		{"unhydrated tool content", []model.Message{native, ref}, "content reference"},
		{"unsupported file id", []model.Message{other, fileID}, "cannot be represented"},
		{"unsupported part", []model.Message{other, {Role: model.RoleUser, ContentParts: []model.ContentPart{{Type: "custom"}}}}, "cannot be represented"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, _, err := (&Model{}).prepareProviderHistory(tt.messages)
			if err == nil {
				_, err = (&Model{}).convertProviderHistory(prepared)
			}
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestResponsesHistoryReplaysLegacyNullToolArguments(t *testing.T) {
	m := &Model{}
	messages := []model.Message{
		responsesHistoryMessage(`[]`, "source answer"),
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{historyCall("c1", "lookup", `null`)}},
		model.NewToolMessage("c1", "lookup", "legacy result"),
		model.NewUserMessage("continue"),
	}
	prepared, projected, err := m.prepareProviderHistory(messages)
	require.NoError(t, err)
	require.True(t, projected)
	contents, err := m.convertProviderHistory(prepared)
	require.NoError(t, err)
	require.Len(t, contents, 4)
	require.Equal(t, "c1", contents[1].Parts[0].FunctionCall.ID)
	require.Equal(t, map[string]any{}, contents[1].Parts[0].FunctionCall.Args)
	require.Equal(t, "c1", contents[2].Parts[0].FunctionResponse.ID)
	require.Equal(t, "legacy result", contents[2].Parts[0].FunctionResponse.Response["output"])
	require.Equal(t, "null", string(messages[1].ToolCalls[0].Function.Arguments))
}

func TestResponsesHistoryRetainsEmptyToolOutputAndLegacyPath(t *testing.T) {
	call := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
	text := " world"
	result := model.NewToolMessage("c1", "lookup", "hello")
	result.ContentParts = []model.ContentPart{{}, {Type: model.ContentTypeText, Text: &text}}
	m := &Model{}
	prepared, projected, err := m.prepareProviderHistory([]model.Message{call, result})
	require.NoError(t, err)
	require.True(t, projected)
	contents, err := m.convertProviderHistory(prepared)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"output": "hello world"}, contents[1].Parts[0].FunctionResponse.Response)

	result.Content = `{}`
	result.ContentParts = nil
	prepared, _, err = m.prepareProviderHistory([]model.Message{call, result})
	require.NoError(t, err)
	contents, err = m.convertProviderHistory(prepared)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"output": "{}"}, contents[1].Parts[0].FunctionResponse.Response)

	legacy := model.NewToolMessage("x", "lookup", "result")
	legacy.AddImageURL("https://example.test/image.png", "")
	unchanged, projected, err := m.prepareProviderHistory([]model.Message{legacy})
	require.NoError(t, err)
	require.False(t, projected)
	require.Equal(t, []model.Message{legacy}, unchanged)
}

func TestResponsesHistoryRejectsLossyNumbersBeforeHTTP(t *testing.T) {
	for _, number := range []string{"9007199254740993", "0.1234567890123456789", "1e400", "1e-9999"} {
		for _, source := range []string{"arguments", "callback"} {
			t.Run(number+"/"+source, func(t *testing.T) {
				call := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
				result := model.NewToolMessage("c1", "lookup", "ok")
				m := &Model{}
				switch source {
				case "arguments":
					call.ToolCalls[0].Function.Arguments = []byte(`{"value":` + number + `}`)
				case "callback":
					m.chatRequestCallback = func(_ context.Context, contents []*genai.Content) {
						contents[0].Parts[0].FunctionCall.Args["value"] = json.Number(number)
					}
				}
				_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{call, result}})
				require.ErrorContains(t, err, "cannot be preserved by Gemini SDK")
			})
		}
	}
}

func TestResponsesHistoryRejectsUnsupportedToolContentAfterTailoring(t *testing.T) {
	call := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
	tailoredCall := model.Message{Role: model.RoleAssistant, ToolCalls: call.ToolCalls}
	tailoredResult := model.NewToolMessage("c1", "lookup", "result")
	tailoredResult.ContentParts = []model.ContentPart{{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}}}
	m := &Model{
		enableTokenTailoring: true,
		maxInputTokens:       100,
		tailoringStrategy:    overflowTailoringStrategy{tailored: []model.Message{tailoredCall, tailoredResult}},
	}
	_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{call, model.NewToolMessage("c1", "lookup", "result")}})
	require.ErrorContains(t, err, "tool result content type")
}

func TestResponsesHistorySendsMediaInOrder(t *testing.T) {
	const audioURL = "https://example.test/clip.wav"
	const videoURL = "https://example.test/clip.mp4"
	const fileURL = "https://example.test/notes.pdf"
	media := model.NewUserMessage("review these attachments")
	imagePath := filepath.Join(t.TempDir(), "receipt.jpg")
	require.NoError(t, os.WriteFile(imagePath, []byte("image"), 0600))
	require.NoError(t, media.AddImageFilePath(imagePath, ""))
	media.AddImageData([]byte("png"), "", "png")
	media.AddAudioURL(audioURL, "wav")
	media.AddAudioData([]byte("audio"), "audio/wav")
	media.AddVideoURL(videoURL, "mp4")
	media.AddVideoData([]byte("video"), "video/mp4")
	media.AddFileURL("notes.pdf", fileURL, "application/pdf")
	media.AddFileData("notes.pdf", []byte("file"), "application/pdf")
	request := &model.Request{Messages: []model.Message{
		media,
		responsesHistoryMessage(`[{"type":"reasoning","id":"r1","encrypted_content":"private"}]`, ""),
		model.NewUserMessage("continue"),
	}}
	server, requests := newHistoryServer(t)
	m, err := New(context.Background(), "gemini-test", WithGeminiClientConfig(&genai.ClientConfig{
		APIKey: "test", Backend: genai.BackendGeminiAPI, HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
	}))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	responses, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)
	for response := range responses {
		require.Nil(t, response.Error)
	}
	require.Len(t, requests, 1)
	captured := <-requests
	require.NoError(t, captured.err)
	var body struct{ Contents json.RawMessage }
	require.NoError(t, json.Unmarshal(captured.body, &body))
	require.JSONEq(t, `[
		{"role":"user","parts":[
			{"text":"review these attachments"},
            {"inlineData":{"data":"aW1hZ2U=","mimeType":"image/jpeg"}},
            {"inlineData":{"data":"cG5n","mimeType":"image/png"}},
			{"fileData":{"fileUri":"https://example.test/clip.wav","mimeType":"audio/wav"}},
			{"inlineData":{"data":"YXVkaW8=","mimeType":"audio/wav"}},
			{"fileData":{"fileUri":"https://example.test/clip.mp4","mimeType":"video/mp4"}},
			{"inlineData":{"data":"dmlkZW8=","mimeType":"video/mp4"}},
			{"text":"File URL: notes.pdf (application/pdf): https://example.test/notes.pdf"},
			{"inlineData":{"data":"ZmlsZQ==","mimeType":"application/pdf"}}
		]},
		{"role":"user","parts":[{"text":"continue"}]}
	]`, string(body.Contents))
}

func TestResponsesHistoryRejectsInvalidMediaAndCallsBeforeDispatch(t *testing.T) {
	native := responsesHistoryMessage(`[]`, "source answer")
	calls := func(role model.Role, id, name string) model.Message {
		return model.Message{Role: role, ToolCalls: []model.ToolCall{historyCall(id, name, "{}")}}
	}
	media := func(part model.ContentPart) model.Message {
		return model.Message{Role: model.RoleUser, ContentParts: []model.ContentPart{part}}
	}
	tests := []struct {
		name    string
		message model.Message
		want    string
	}{
		{"missing call ID", calls(model.RoleAssistant, "", "lookup"), "empty call_id"},
		{"missing function name", calls(model.RoleAssistant, "c1", ""), `invalid function call "c1"`},
		{"call in user message", calls(model.RoleUser, "c1", "lookup"), "function calls require the assistant role"},
		{"unknown role", calls("custom", "c1", "lookup"), `unsupported role "custom"`},
		{"audio without source", media(model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{Format: "wav"}}), `content type "audio" cannot be represented`},
		{"video without source", media(model.ContentPart{Type: model.ContentTypeVideo, Video: &model.Video{Format: "mp4"}}), `content type "video" cannot be represented`},
		{"file without source", media(model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "notes.pdf"}}), `content type "file" cannot be represented`},
		{"audio with video payload", media(model.ContentPart{Type: model.ContentTypeAudio, Video: &model.Video{URL: "https://example.test/clip.mp4"}}), "mismatched payload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, afterTailoring := range []bool{false, true} {
				t.Run(fmt.Sprintf("afterTailoring=%t", afterTailoring), func(t *testing.T) {
					m := &Model{client: NewMockClient(gomock.NewController(t))}
					messages := []model.Message{native, tt.message}
					if afterTailoring {
						m.enableTokenTailoring = true
						m.maxInputTokens = 100
						m.tailoringStrategy = overflowTailoringStrategy{tailored: []model.Message{tt.message}}
						messages = []model.Message{native}
					}
					responses, err := m.GenerateContent(context.Background(), &model.Request{Messages: messages})
					require.ErrorContains(t, err, tt.want)
					require.Nil(t, responses)
				})
			}
		})
	}
}
