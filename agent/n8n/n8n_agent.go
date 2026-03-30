//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package n8n provides an agent that communicates with n8n workflows via webhook.
package n8n

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultStreamingChannelSize    = 1024
	defaultNonStreamingChannelSize = 10
	// n8n workflows can involve multiple external API calls and AI model invocations,
	// so a generous default timeout is used to avoid premature cancellation.
	defaultHTTPTimeout  = time.Hour
	maxResponseBodySize = 1 << 20 // 1MB limit for response body reads
)

// N8nAgent is an agent that communicates with a remote n8n webhook.
type N8nAgent struct {
	webhookURL           string
	name                 string
	description          string
	authType             AuthType
	authConfig           *AuthConfig
	requestConverter     RequestConverter
	responseConverter    ResponseConverter
	streamingBufSize     int
	streamingRespHandler StreamingRespHandler
	enableStreaming      *bool
	httpClient           *http.Client
	getHTTPClientFunc    func(*agent.Invocation) (*http.Client, error)
	transferStateKey     []string
}

// New creates a new N8nAgent with the given options.
func New(opts ...Option) (*N8nAgent, error) {
	a := &N8nAgent{
		requestConverter:  &defaultRequestConverter{},
		responseConverter: &defaultResponseConverter{},
		streamingBufSize:  defaultStreamingChannelSize,
		authType:          AuthNone,
	}

	for _, opt := range opts {
		opt(a)
	}

	if a.name == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if a.webhookURL == "" {
		return nil, fmt.Errorf("webhook URL is required")
	}

	// Initialize default HTTP client if none provided, so the connection pool is reused.
	if a.httpClient == nil && a.getHTTPClientFunc == nil {
		a.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	return a, nil
}

// Run implements the Agent interface.
func (a *N8nAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if a.shouldUseStreaming(invocation) {
		return a.runStreaming(ctx, invocation)
	}
	return a.runNonStreaming(ctx, invocation)
}

// Tools implements the Agent interface.
func (a *N8nAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// Info implements the Agent interface.
func (a *N8nAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// SubAgents implements the Agent interface.
func (a *N8nAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

// FindSubAgent implements the Agent interface.
func (a *N8nAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (a *N8nAgent) shouldUseStreaming(invocation *agent.Invocation) bool {
	if invocation != nil && invocation.RunOptions.Stream != nil {
		return *invocation.RunOptions.Stream
	}
	if a.enableStreaming != nil {
		return *a.enableStreaming
	}
	return false
}

func (a *N8nAgent) getHTTPClient(invocation *agent.Invocation) (*http.Client, error) {
	if a.getHTTPClientFunc != nil {
		client, err := a.getHTTPClientFunc(invocation)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, fmt.Errorf("getHTTPClientFunc returned nil client")
		}
		return client, nil
	}
	return a.httpClient, nil
}

// buildHTTPRequest constructs the HTTP request for the n8n webhook.
func (a *N8nAgent) buildHTTPRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStreaming bool,
) (*http.Request, error) {
	if a.requestConverter == nil {
		return nil, fmt.Errorf("request converter not set")
	}

	body, err := a.requestConverter.ConvertToN8nRequest(ctx, invocation)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %v", err)
	}
	if body == nil {
		body = map[string]any{}
	}

	if len(a.transferStateKey) > 0 {
		var inputs map[string]any
		if existing, ok := body["inputs"]; ok {
			inputs, ok = existing.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("body[\"inputs\"] has unexpected type %T, expected map[string]any", existing)
			}
		}
		if inputs == nil {
			inputs = map[string]any{}
		}
		for _, key := range a.transferStateKey {
			if value, ok := invocation.RunOptions.RuntimeState[key]; ok {
				inputs[key] = value
			}
		}
		body["inputs"] = inputs
	}

	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.webhookURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
	}

	a.applyAuth(req)

	return req, nil
}

func (a *N8nAgent) applyAuth(req *http.Request) {
	if a.authConfig == nil {
		return
	}
	switch a.authType {
	case AuthBasic:
		req.SetBasicAuth(a.authConfig.Username, a.authConfig.Password)
	case AuthHeader:
		headerName := a.authConfig.HeaderName
		if headerName == "" {
			headerName = "Authorization"
		}
		req.Header.Set(headerName, a.authConfig.HeaderValue)
	}
}

func (a *N8nAgent) sendErrorEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	errorMessage string,
) {
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		a.name,
		event.WithResponse(&model.Response{
			Error: &model.ResponseError{
				Message: errorMessage,
			},
		}),
	))
}

func (a *N8nAgent) sendFinalStreamingEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	aggregatedContent string,
) {
	now := time.Now()
	message := model.Message{
		Role:    model.RoleAssistant,
		Content: aggregatedContent,
	}
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		a.name,
		event.WithResponse(&model.Response{
			Done:      true,
			IsPartial: false,
			Timestamp: now,
			Created:   now.Unix(),
			Choices:   []model.Choice{{Message: message, Delta: message}},
		}),
	))
}

func (a *N8nAgent) runNonStreaming(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultNonStreamingChannelSize)

	go func() {
		defer close(eventChan)

		req, err := a.buildHTTPRequest(ctx, invocation, false)
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, err.Error())
			return
		}

		client, err := a.getHTTPClient(invocation)
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to get HTTP client: %v", err))
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("HTTP request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to read response body: %v", err))
			return
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			a.sendErrorEvent(ctx, eventChan, invocation,
				fmt.Sprintf("n8n webhook returned status %d: %s", resp.StatusCode, string(body)))
			return
		}

		evt := a.responseConverter.ConvertToEvent(body, a.name, invocation)
		agent.EmitEvent(ctx, invocation, eventChan, evt)
	}()

	return eventChan, nil
}

func (a *N8nAgent) runStreaming(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if a.responseConverter == nil {
		return nil, fmt.Errorf("response converter not set")
	}

	eventChan := make(chan *event.Event, a.streamingBufSize)

	go func() {
		defer close(eventChan)

		req, err := a.buildHTTPRequest(ctx, invocation, true)
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, err.Error())
			return
		}

		client, err := a.getHTTPClient(invocation)
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to get HTTP client: %v", err))
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			a.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("HTTP request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
			a.sendErrorEvent(ctx, eventChan, invocation,
				fmt.Sprintf("n8n webhook returned status %d: %s", resp.StatusCode, string(body)))
			return
		}

		var aggregatedContentBuilder strings.Builder
		// Use bufio.Reader instead of Scanner to avoid line size limits on large SSE chunks.
		reader := bufio.NewReader(resp.Body)
		for {
			if err := agent.CheckContextCancelled(ctx); err != nil {
				return
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				a.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to read streaming response: %v", err))
				return
			}

			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			// Per SSE spec, if the value starts with a space, remove it (exactly one).
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			if data == "[DONE]" {
				break
			}

			evt := a.responseConverter.ConvertStreamingToEvent([]byte(data), a.name, invocation)
			if evt == nil {
				continue
			}

			if evt.Response != nil && len(evt.Response.Choices) > 0 {
				if a.streamingRespHandler != nil {
					content, err := a.streamingRespHandler(evt.Response)
					if err != nil {
						// Handler error is treated as fatal: partial aggregated content is
						// intentionally discarded because the handler may have encountered
						// malformed data, making the accumulated content unreliable.
						a.sendErrorEvent(ctx, eventChan, invocation,
							fmt.Sprintf("streaming resp handler failed: %v", err))
						return
					}
					aggregatedContentBuilder.WriteString(content)
				} else if evt.Response.Choices[0].Delta.Content != "" {
					aggregatedContentBuilder.WriteString(evt.Response.Choices[0].Delta.Content)
				}
			}

			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}

		a.sendFinalStreamingEvent(ctx, eventChan, invocation, aggregatedContentBuilder.String())
	}()

	return eventChan, nil
}
