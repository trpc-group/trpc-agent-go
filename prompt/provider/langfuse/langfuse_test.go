//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse/config"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := NewClient(config.ConnectionConfig{
		PublicKey: "pk-test",
		SecretKey: "sk-test",
		BaseURL:   srv.URL,
	})
	return srv, client
}

func TestNewClient_DefaultHTTPTimeout(t *testing.T) {
	client := NewClient(config.ConnectionConfig{
		PublicKey: "pk-test",
		SecretKey: "sk-test",
		BaseURL:   "https://example.com",
	})

	require.NotNil(t, client.httpClient)
	assert.Equal(t, defaultHTTPTimeout, client.httpClient.Timeout)
}

func TestFetchTextPrompt_Success(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/public/v2/prompts/movie-critic", r.URL.Path)
		assert.Equal(t, "production", r.URL.Query().Get("label"))

		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "pk-test", user)
		assert.Equal(t, "sk-test", pass)

		resp := apiPromptResponse{
			Name:    "movie-critic",
			Version: 3,
			Type:    "text",
			Prompt:  "As a {{criticLevel}} movie critic, do you like {{movie}}?",
			Config:  map[string]any{"model": "gpt-4o", "temperature": 0.7},
			Labels:  []string{"production", "latest"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	result, err := client.FetchTextPrompt(context.Background(), "movie-critic")
	require.NoError(t, err)

	assert.Equal(t, "As a {{criticLevel}} movie critic, do you like {{movie}}?", result.Text.Template)
	assert.Equal(t, prompt.SyntaxDoubleBrace, result.Text.Syntax)
	assert.Equal(t, "movie-critic", result.Text.Meta.Name)
	assert.Equal(t, "3", result.Text.Meta.Version)
	assert.Equal(t, 3, result.Version)
	assert.Equal(t, []string{"production", "latest"}, result.Labels)
	assert.Equal(t, "gpt-4o", result.Config["model"])
}

func TestFetchTextPrompt_EncodesPromptNameWithFolders(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/public/v2/prompts/folder%2Fsubfolder%2Fprompt-name?label=production", r.RequestURI)
		resp := apiPromptResponse{
			Name:    "folder/subfolder/prompt-name",
			Version: 1,
			Type:    "text",
			Prompt:  "hi",
		}
		json.NewEncoder(w).Encode(resp)
	})

	_, err := client.FetchTextPrompt(context.Background(), "folder/subfolder/prompt-name")
	require.NoError(t, err)
}

func TestFetchTextPrompt_WithLabel(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "staging", r.URL.Query().Get("label"))
		resp := apiPromptResponse{Name: "p", Version: 1, Type: "text", Prompt: "hi"}
		json.NewEncoder(w).Encode(resp)
	})

	_, err := client.FetchTextPrompt(context.Background(), "p", WithLabel("staging"))
	require.NoError(t, err)
}

func TestFetchTextPrompt_WithVersion(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "5", r.URL.Query().Get("version"))
		assert.Empty(t, r.URL.Query().Get("label"))
		resp := apiPromptResponse{Name: "p", Version: 5, Type: "text", Prompt: "v5"}
		json.NewEncoder(w).Encode(resp)
	})

	_, err := client.FetchTextPrompt(context.Background(), "p", WithVersion(5))
	require.NoError(t, err)
}

func TestFetchTextPrompt_MixedSelectorOrder(t *testing.T) {
	t.Run("label overrides earlier version", func(t *testing.T) {
		_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "staging", r.URL.Query().Get("label"))
			assert.Empty(t, r.URL.Query().Get("version"))
			resp := apiPromptResponse{Name: "p", Version: 2, Type: "text", Prompt: "staging"}
			json.NewEncoder(w).Encode(resp)
		})

		_, err := client.FetchTextPrompt(context.Background(), "p", WithVersion(5), WithLabel("staging"))
		require.NoError(t, err)
	})

	t.Run("version overrides earlier label", func(t *testing.T) {
		_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "5", r.URL.Query().Get("version"))
			assert.Empty(t, r.URL.Query().Get("label"))
			resp := apiPromptResponse{Name: "p", Version: 5, Type: "text", Prompt: "v5"}
			json.NewEncoder(w).Encode(resp)
		})

		_, err := client.FetchTextPrompt(context.Background(), "p", WithLabel("staging"), WithVersion(5))
		require.NoError(t, err)
	})
}

func TestFetchTextPrompt_ChatTypeRejected(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := apiPromptResponse{
			Name:    "chat-prompt",
			Version: 1,
			Type:    "chat",
			Prompt:  []any{map[string]any{"role": "system", "content": "hi"}},
		}
		json.NewEncoder(w).Encode(resp)
	})

	_, err := client.FetchTextPrompt(context.Background(), "chat-prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected \"text\"")
}

func TestFetchTextPrompt_HTTPError(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})

	_, err := client.FetchTextPrompt(context.Background(), "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestFetchTextPrompt_Render(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := apiPromptResponse{
			Name:    "greet",
			Version: 1,
			Type:    "text",
			Prompt:  "Hello {{name}}, welcome to {{place}}!",
		}
		json.NewEncoder(w).Encode(resp)
	})

	result, err := client.FetchTextPrompt(context.Background(), "greet")
	require.NoError(t, err)

	rendered, err := result.Text.Render(prompt.RenderEnv{
		Vars: prompt.Vars{"name": "Alice", "place": "Wonderland"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello Alice, welcome to Wonderland!", rendered)
}

func TestTextPromptSource_Caching(t *testing.T) {
	callCount := 0
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := apiPromptResponse{
			Name:    "cached",
			Version: callCount,
			Type:    "text",
			Prompt:  "v" + json.Number(rune('0'+callCount)).String(),
		}
		json.NewEncoder(w).Encode(resp)
	})

	source := client.TextPromptSourceWithOptions(
		"cached",
		[]FetchOption{WithLabel("production")},
		WithCacheTTL(50*time.Millisecond),
	)

	t1, err := source.FetchPrompt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1", t1.Meta.Version)

	t2, err := source.FetchPrompt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1", t2.Meta.Version)
	assert.Equal(t, 1, callCount, "second call should use cache")

	time.Sleep(60 * time.Millisecond)

	t3, err := source.FetchPrompt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "2", t3.Meta.Version)
	assert.Equal(t, 2, callCount, "cache expired, should re-fetch")
}

func TestTextPromptSource_StaleOnError(t *testing.T) {
	shouldFail := false
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if shouldFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := apiPromptResponse{Name: "p", Version: 1, Type: "text", Prompt: "ok"}
		json.NewEncoder(w).Encode(resp)
	})

	source := client.TextPromptSourceWithOptions(
		"p",
		nil,
		WithCacheTTL(10*time.Millisecond),
	)

	t1, err := source.FetchPrompt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", t1.Template)

	shouldFail = true
	time.Sleep(15 * time.Millisecond)

	t2, err := source.FetchPrompt(context.Background())
	require.NoError(t, err, "should return stale cache on error")
	assert.Equal(t, "ok", t2.Template)
}

func TestTextPromptSource_CanceledContextDoesNotReturnStale(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := apiPromptResponse{Name: "p", Version: 1, Type: "text", Prompt: "ok"}
		json.NewEncoder(w).Encode(resp)
	})

	source := client.TextPromptSourceWithOptions(
		"p",
		nil,
		WithCacheTTL(10*time.Millisecond),
	)

	t1, err := source.FetchPrompt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", t1.Template)

	time.Sleep(15 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t2, err := source.FetchPrompt(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, prompt.Text{}, t2)
}

func TestTextPromptSource_ExpiredDeadlineDoesNotReturnStale(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := apiPromptResponse{Name: "p", Version: 1, Type: "text", Prompt: "ok"}
		json.NewEncoder(w).Encode(resp)
	})

	source := client.TextPromptSourceWithOptions(
		"p",
		nil,
		WithCacheTTL(10*time.Millisecond),
	)

	t1, err := source.FetchPrompt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", t1.Template)

	time.Sleep(15 * time.Millisecond)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	t2, err := source.FetchPrompt(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, prompt.Text{}, t2)
}

func TestTextPromptSource_ErrorWithNoCache(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	source := client.TextPromptSource("p")
	_, err := source.FetchPrompt(context.Background())
	require.Error(t, err)
}
