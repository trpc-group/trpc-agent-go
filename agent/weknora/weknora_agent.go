//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package weknora provides an agent that can communicate with WeKnora service.
package weknora

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/client"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultStreamingChannelSize = 1024
)

// WeKnoraAgent is an agent that communicates with a remote WeKnora service.
type WeKnoraAgent struct {
	baseUrl          string // weknora base url
	token            string // weknora token
	name             string
	description      string
	agentID          string
	timeout          time.Duration
	knowledgeBaseIDs []string
	webSearchEnabled bool

	weknoraClient        *client.Client
	getWeKnoraClientFunc func(*agent.Invocation) (*client.Client, error)
}

// New creates a new WeKnoraAgent.
func New(opts ...Option) (*WeKnoraAgent, error) {
	weknoraAgent := &WeKnoraAgent{}

	for _, opt := range opts {
		opt(weknoraAgent)
	}

	if weknoraAgent.name == "" {
		return nil, fmt.Errorf("agent name is required")
	}

	return weknoraAgent, nil
}

// sendErrorEvent sends an error event to the event channel
func (r *WeKnoraAgent) sendErrorEvent(ctx context.Context, eventChan chan<- *event.Event,
	invocation *agent.Invocation, errorMessage string) {
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			Error: &model.ResponseError{
				Message: errorMessage,
			},
		}),
	))
}

// Run implements the Agent interface
func (r *WeKnoraAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation != nil && invocation.RunOptions.Stream != nil && !*invocation.RunOptions.Stream {
		return nil, fmt.Errorf("weknora agent only supports streaming")
	}
	cli, err := r.getWeKnoraClient(invocation)
	if err != nil {
		return nil, err
	}
	r.weknoraClient = cli

	return r.runStreaming(ctx, invocation)
}

// buildWeKnoraRequest constructs WeKnora request from invocation
func (r *WeKnoraAgent) buildWeKnoraRequest(
	ctx context.Context,
	invocation *agent.Invocation,
) (*client.AgentQARequest, error) {
	query := invocation.Message.Content
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	req := &client.AgentQARequest{
		Query:            query,
		AgentEnabled:     true,
		AgentID:          r.agentID,
		KnowledgeBaseIDs: r.knowledgeBaseIDs,
		WebSearchEnabled: r.webSearchEnabled,
	}

	return req, nil
}

// runStreaming handles streaming communication
func (r *WeKnoraAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultStreamingChannelSize)

	req, err := r.buildWeKnoraRequest(ctx, invocation)
	if err != nil {
		return nil, fmt.Errorf("failed to construct WeKnora request: %v", err)
	}

	sessionID := "default-session"

	if invocation.Session != nil && invocation.SessionService != nil {
		state, err := invocation.SessionService.ListUserStates(ctx, session.UserKey{
			AppName: invocation.Session.AppName,
			UserID:  invocation.Session.UserID,
		})
		if err == nil {
			if sessionIDBytes, ok := state[genSessionKey(invocation.Session.ID)]; ok {
				sessionID = string(sessionIDBytes)
			}
		}
	}

	_, err = r.weknoraClient.GetSession(ctx, sessionID)
	if err != nil {
		// Session might not exist, try to create it
		newSession, createErr := r.weknoraClient.CreateSession(ctx, &client.CreateSessionRequest{
			Title:       "Auto-created session",
			Description: "Session automatically created by trpc-agent-go",
		})
		if createErr != nil {
			return nil, fmt.Errorf("failed to get or create session: get err: %v, create err: %v", err, createErr)
		}
		sessionID = newSession.ID
		if invocation.Session != nil && invocation.SessionService != nil {
			invocation.SessionService.UpdateUserState(ctx, session.UserKey{
				AppName: invocation.Session.AppName,
				UserID:  invocation.Session.UserID,
			}, session.StateMap{
				genSessionKey(invocation.Session.ID): []byte(sessionID),
			})
		}
	}

	go func() {
		defer close(eventChan)

		var aggregatedContentBuilder strings.Builder
		var aggregatedReasoningBuilder strings.Builder

		err := r.weknoraClient.AgentQAStreamWithRequest(ctx, sessionID, req, func(resp *client.AgentStreamResponse) error {
			if err := agent.CheckContextCancelled(ctx); err != nil {
				return err
			}

			if resp.ResponseType == client.AgentResponseTypeAnswer || resp.ResponseType == client.AgentResponseTypeThinking {
				if resp.Content != "" {
					message := model.Message{
						Role: model.RoleAssistant,
					}

					if resp.ResponseType == client.AgentResponseTypeAnswer {
						aggregatedContentBuilder.WriteString(resp.Content)
						message.Content = resp.Content
					} else {
						aggregatedReasoningBuilder.WriteString(resp.Content)
						message.ReasoningContent = resp.Content
					}

					evt := event.New(
						invocation.InvocationID,
						r.name,
						event.WithResponse(&model.Response{
							Object:    model.ObjectTypeChatCompletionChunk,
							Choices:   []model.Choice{{Delta: message}},
							Timestamp: time.Now(),
							Created:   time.Now().Unix(),
							IsPartial: true,
							Done:      false,
						}),
						event.WithObject(model.ObjectTypeChatCompletionChunk),
					)
					agent.EmitEvent(ctx, invocation, eventChan, evt)
				}
			} else if resp.ResponseType == client.AgentResponseTypeError {
				return fmt.Errorf("weknora agent error: %s", resp.Content)
			}

			return nil
		})

		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, err.Error())
			return
		}

		// Send final aggregated event
		r.sendFinalStreamingEvent(ctx, eventChan, invocation, aggregatedContentBuilder.String(), aggregatedReasoningBuilder.String())
	}()

	return eventChan, nil
}

// sendFinalStreamingEvent sends the final aggregated event for streaming
func (r *WeKnoraAgent) sendFinalStreamingEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	aggregatedContent string,
	aggregatedReasoning string,
) {
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			Done:      true,
			IsPartial: false,
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			Choices: []model.Choice{{
				Message: model.Message{
					Role:             model.RoleAssistant,
					Content:          aggregatedContent,
					ReasoningContent: aggregatedReasoning,
				},
			}},
		}),
	))
}

// Tools implements the Agent interface
func (r *WeKnoraAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// Info implements the Agent interface
func (r *WeKnoraAgent) Info() agent.Info {
	return agent.Info{
		Name:        r.name,
		Description: r.description,
	}
}

// SubAgents implements the Agent interface
func (r *WeKnoraAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

// FindSubAgent implements the Agent interface
func (r *WeKnoraAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (r *WeKnoraAgent) getWeKnoraClient(
	invocation *agent.Invocation,
) (*client.Client, error) {
	if r.getWeKnoraClientFunc != nil {
		return r.getWeKnoraClientFunc(invocation)
	}

	opts := []client.ClientOption{}
	if r.token != "" {
		opts = append(opts, client.WithToken(r.token))
	}
	if r.timeout.Seconds() > 0 {
		opts = append(opts, client.WithTimeout(r.timeout))
	}

	return client.NewClient(r.baseUrl, opts...), nil
}

func genSessionKey(sessionID string) string {
	return fmt.Sprintf("weknora_session_%s", sessionID)
}
