package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestGenaiDirectExtrasRequestProvider(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		captured = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	t.Cleanup(server.Close)

	ctx := t.Context()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    server.URL,
			APIVersion: "v1beta",
		},
	})
	require.NoError(t, err)

	contents := []*genai.Content{genai.NewContentFromParts([]*genai.Part{{
		FunctionCall:     &genai.FunctionCall{Name: "tool", Args: map[string]any{"x": "y"}},
		ThoughtSignature: []byte(geminiSkipThoughtSignatureValidator),
	}}, genai.RoleModel)}

	_, err = client.Models.GenerateContent(ctx, "gemini-3-flash-preview", contents, &genai.GenerateContentConfig{
		HTTPOptions: &genai.HTTPOptions{
			ExtrasRequestProvider: rewriteBypassThoughtSignatures,
		},
	})
	require.NoError(t, err)
	assert.Contains(t, string(captured), `"thoughtSignature":"skip_thought_signature_validator"`)
}
