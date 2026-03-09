//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates that a sub-agent's skill_load does not leak
// loaded skill bodies/docs into the coordinator's prompt.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

const (
	appName = "skillisolation-example"
	userID  = "skillisolation-user"

	coordinatorAgentName = "skillisolation-coordinator"
	childAgentName       = "skillisolation-child"

	demoSkillName      = "demo-skill"
	defaultSkillsRoot  = "./skills"
	defaultModelName   = "gpt-5"
	loadedMarkerPrefix = "[Loaded] "
	loadedMarker       = loadedMarkerPrefix + demoSkillName
)

func main() {
	var (
		flagModel = flag.String(
			"model",
			defaultModelName,
			"OpenAI-compatible model name",
		)
		flagSkillsRoot = flag.String(
			"skills-root",
			defaultSkillsRoot,
			"Skills root directory",
		)
	)
	flag.Parse()

	repo, err := skill.NewFSRepository(*flagSkillsRoot)
	if err != nil {
		log.Fatalf("load skills repo: %v", err)
	}

	mdl := openai.New(*flagModel)

	child := llmagent.New(
		childAgentName,
		llmagent.WithModel(mdl),
		llmagent.WithSkills(repo),
		llmagent.WithInstruction(childInstruction()),
		llmagent.WithInputSchema(agentToolInputSchema()),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: false,
		}),
	)
	childTool := agenttool.NewTool(child)

	coordinator := llmagent.New(
		coordinatorAgentName,
		llmagent.WithModel(mdl),
		llmagent.WithSkills(repo),
		llmagent.WithTools([]tool.Tool{childTool}),
		llmagent.WithInstruction(coordinatorInstruction()),
		llmagent.WithInputSchema(agentToolInputSchema()),
		llmagent.WithModelCallbacks(coordinatorCallbacks()),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: false,
		}),
	)

	svc := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		coordinator,
		runner.WithSessionService(svc),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("skillisolation-%d", time.Now().Unix())
	fmt.Printf("Session: %s\n", sessionID)

	ctx := context.Background()
	msg := model.NewUserMessage(
		"Call the sub-agent tool. " +
			"The sub-agent must load demo-skill via skill_load.",
	)
	events, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	printTranscript(events)

	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		log.Fatalf("get session: %v", err)
	}

	printStateSummary(sess)
}

func coordinatorCallbacks() *model.Callbacks {
	var call int
	return model.NewCallbacks().RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		call++
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil || inv.Session == nil {
			return nil, nil
		}
		sys := systemMessage(args.Request.Messages)
		hasLoaded := strings.Contains(sys, loadedMarker)

		fmt.Printf("\n[coordinator before model #%d]\n", call)
		fmt.Printf("has %q in system: %t\n", loadedMarker, hasLoaded)
		fmt.Printf("loaded skills (coordinator): %v\n",
			loadedSkillNames(inv, inv.AgentName),
		)
		fmt.Printf("loaded skills (child): %v\n",
			loadedSkillNames(inv, childAgentName),
		)
		return nil, nil
	})
}

func systemMessage(msgs []model.Message) string {
	for _, msg := range msgs {
		if msg.Role == model.RoleSystem {
			return msg.Content
		}
	}
	return ""
}

func loadedSkillNames(
	inv *agent.Invocation,
	agentName string,
) []string {
	if inv == nil || inv.Session == nil {
		return nil
	}
	state := inv.Session.SnapshotState()
	if len(state) == 0 {
		return nil
	}

	prefix := skill.LoadedPrefix(agentName)

	var out []string
	for k, v := range state {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if len(v) == 0 {
			continue
		}
		name := strings.TrimPrefix(k, prefix)
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func printStateSummary(sess *session.Session) {
	if sess == nil {
		return
	}

	childKey := skill.LoadedKey(childAgentName, demoSkillName)
	coordKey := skill.LoadedKey(coordinatorAgentName, demoSkillName)

	hasChild := hasStateKey(sess.State, childKey)
	hasCoord := hasStateKey(sess.State, coordKey)

	fmt.Printf("\n[state summary]\n")
	fmt.Printf("child loaded key present: %t\n", hasChild)
	fmt.Printf("coordinator loaded key present: %t\n", hasCoord)
	fmt.Printf("skill state keys: %v\n", listSkillStateKeys(sess.State))
}

func hasStateKey(state session.StateMap, key string) bool {
	if len(state) == 0 || strings.TrimSpace(key) == "" {
		return false
	}
	v, ok := state[key]
	return ok && len(v) > 0
}

func listSkillStateKeys(state session.StateMap) []string {
	if len(state) == 0 {
		return nil
	}
	var out []string
	for k := range state {
		if strings.HasPrefix(k, skill.StateKeyLoadedPrefix) ||
			strings.HasPrefix(k, skill.StateKeyDocsPrefix) ||
			strings.HasPrefix(k, skill.StateKeyLoadedByAgentPrefix) ||
			strings.HasPrefix(k, skill.StateKeyDocsByAgentPrefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func drain(events <-chan *event.Event) {
	for range events {
	}
}

func printTranscript(events <-chan *event.Event) {
	for evt := range events {
		printEvent(evt)
	}
}

func printEvent(evt *event.Event) {
	if evt != nil && evt.Error != nil {
		fmt.Printf("error: %s\n", strings.TrimSpace(evt.Error.Message))
		return
	}
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	if evt.Object == model.ObjectTypeStateUpdate {
		fmt.Println("state.update")
		return
	}
	ch := evt.Response.Choices[0]
	msg := ch.Message
	delta := ch.Delta
	switch {
	case len(msg.ToolCalls) > 0:
		fmt.Println("tool calls:")
		for _, tc := range msg.ToolCalls {
			fmt.Printf("  - %s id=%s args=%s\n",
				tc.Function.Name,
				tc.ID,
				string(tc.Function.Arguments),
			)
		}
	case len(delta.ToolCalls) > 0:
		fmt.Println("tool calls (delta):")
		for _, tc := range delta.ToolCalls {
			fmt.Printf("  - %s id=%s args=%s\n",
				tc.Function.Name,
				tc.ID,
				string(tc.Function.Arguments),
			)
		}
	case msg.Role == model.RoleTool:
		fmt.Printf("tool result (%s): %s\n",
			msg.ToolName,
			strings.TrimSpace(msg.Content),
		)
	case strings.TrimSpace(delta.Content) != "":
		fmt.Printf("assistant (delta): %s\n",
			strings.TrimSpace(delta.Content),
		)
	case msg.Role == model.RoleAssistant && msg.Content != "":
		fmt.Printf("assistant: %s\n", strings.TrimSpace(msg.Content))
	}
}

func childInstruction() string {
	return strings.TrimSpace(`
You are a sub-agent.

You will receive a JSON request like {"request":"..."}.

Rules:
1) You MUST call skill_load with {"skill":"demo-skill"}.
2) After the tool returns, reply with exactly: child_done
3) Do not call any other tools.
`)
}

func coordinatorInstruction() string {
	return strings.TrimSpace(`
You are the coordinator agent.

Rules:
1) You MUST call the sub-agent tool "skillisolation-child" exactly once.
2) After the tool returns, reply with exactly: coordinator_done
3) Do not call skill_load or skill_select_docs yourself.
`)
}

func agentToolInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request": map[string]any{
				"type":        "string",
				"description": "Request string for the agent",
			},
		},
		"required": []any{"request"},
	}
}
