//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/messageorigin"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

func TestEventPluginsPreserveInputSource(t *testing.T) {
	for _, source := range []messageorigin.Source{messageorigin.UserInput, messageorigin.Agent, messageorigin.Unknown} {
		for _, mode := range []string{"replace", "in-place", "nil", "error"} {
			name := string(source)
			if source == messageorigin.Unknown {
				name = "unknown"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				original := event.NewResponseEvent("inv", "user", &model.Response{
					Choices: []model.Choice{{Message: model.NewUserMessage("input")}},
				})
				original.Extensions = map[string]json.RawMessage{"original": json.RawMessage(`true`)}
				messageorigin.MarkInput(original, source)
				p := &testPlugin{name: "replace-input", reg: func(registry *plugin.Registry) {
					registry.OnEvent(func(_ context.Context, _ *agent.Invocation, evt *event.Event) (*event.Event, error) {
						updated := evt
						if mode == "replace" {
							updated = &event.Event{Response: evt.Response.Clone(), Author: evt.Author}
						}
						updated.Extensions = map[string]json.RawMessage{"plugin": json.RawMessage(`true`)}
						// Replacing input must not promote imported or delegated
						// messages to host authorization, or demote host input.
						if source == messageorigin.UserInput {
							messageorigin.MarkInput(updated, messageorigin.Agent)
						} else {
							messageorigin.MarkInput(updated, messageorigin.UserInput)
						}
						switch mode {
						case "nil":
							return nil, nil
						case "error":
							return nil, errors.New("plugin failed")
						default:
							return updated, nil
						}
					})
				}}
				inv := &agent.Invocation{Plugins: plugin.MustNewManager(p)}
				out := (&runner{}).applyEventPluginsNoSpan(context.Background(), inv, original)
				require.Equal(t, source, messageorigin.InputSource(out))
				require.JSONEq(t, `true`, string(out.Extensions["plugin"]))
				require.NotContains(t, out.Extensions, "original", "unrelated extensions remain plugin-owned")
			})
		}
	}
}

func TestEventPluginsLeaveUnmarkedEventsUnmarked(t *testing.T) {
	for _, message := range []model.Message{model.NewUserMessage("legacy input"), model.NewAssistantMessage("output")} {
		t.Run(string(message.Role), func(t *testing.T) {
			original := event.NewResponseEvent("inv", "author", &model.Response{Choices: []model.Choice{{Message: message}}})
			p := &testPlugin{name: "replace", reg: func(registry *plugin.Registry) {
				registry.OnEvent(func(_ context.Context, _ *agent.Invocation, evt *event.Event) (*event.Event, error) {
					return &event.Event{Response: evt.Response.Clone()}, nil
				})
			}}
			inv := &agent.Invocation{Plugins: plugin.MustNewManager(p)}
			out := (&runner{}).applyEventPluginsNoSpan(context.Background(), inv, original)
			require.Nil(t, out.Extensions)
		})
	}
}
