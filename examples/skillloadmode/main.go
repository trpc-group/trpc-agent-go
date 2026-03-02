//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how SkillLoadMode affects skill load lifetime.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	appName       = "skillloadmode-example"
	agentName     = "skillloadmode-agent"
	userID        = "skillloadmode-user"
	skillName     = "demo-skill"
	defaultMode   = llmagent.SkillLoadModeTurn
	defaultRoot   = "./skills"
	stateValueOne = "1"
)

func main() {
	var (
		flagMode = flag.String(
			"mode",
			defaultMode,
			"SkillLoadMode: once | turn | session",
		)
		flagSkillsRoot = flag.String(
			"skills-root",
			defaultRoot,
			"Skills root directory",
		)
		flagToolResults = flag.Bool(
			"tool-results",
			false,
			"Materialize loaded skill content into tool results",
		)
	)
	flag.Parse()

	mode := strings.ToLower(strings.TrimSpace(*flagMode))
	if mode != llmagent.SkillLoadModeOnce &&
		mode != llmagent.SkillLoadModeTurn &&
		mode != llmagent.SkillLoadModeSession {
		log.Fatalf("invalid -mode: %q", mode)
	}

	repo, err := skill.NewFSRepository(*flagSkillsRoot)
	if err != nil {
		log.Fatalf("load skills repo: %v", err)
	}

	mock := newStepModel(skillName)
	agt := llmagent.New(
		agentName,
		llmagent.WithModel(mock),
		llmagent.WithSkills(repo),
		llmagent.WithSkillLoadMode(mode),
		llmagent.WithSkillsLoadedContentInToolResults(*flagToolResults),
	)

	svc := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(svc),
	)
	defer r.Close()

	sessionID := fmt.Sprintf(
		"skillloadmode-%d",
		time.Now().Unix(),
	)

	fmt.Printf("SkillLoadMode: %s\n", mode)
	fmt.Printf("Tool result materialization: %t\n", *flagToolResults)
	fmt.Printf("Skills root: %s\n", *flagSkillsRoot)
	fmt.Printf("Session: %s\n\n", sessionID)

	ctx := context.Background()

	fmt.Println("Turn 1: model calls skill_load")
	runOnce(ctx, r, sessionID)
	printSkillState(ctx, svc, sessionID)

	fmt.Println("\nTurn 2: no tool calls (observe auto-clearing)")
	runOnce(ctx, r, sessionID)
	printSkillState(ctx, svc, sessionID)
}

func runOnce(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
) {
	msg := model.NewUserMessage("demo")
	events, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	for evt := range events {
		printSelectedEvent(evt)
	}
}

func printSelectedEvent(evt *event.Event) {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	if evt.Object == model.ObjectTypeStateUpdate {
		fmt.Println("state.update")
		return
	}

	ch := evt.Response.Choices[0]
	if len(ch.Message.ToolCalls) > 0 {
		fmt.Println("tool calls:")
		for _, tc := range ch.Message.ToolCalls {
			fmt.Printf("  - %s id=%s args=%s\n",
				tc.Function.Name,
				tc.ID,
				string(tc.Function.Arguments),
			)
		}
		return
	}
	if ch.Message.Role == model.RoleTool && ch.Message.Content != "" {
		content := strings.TrimSpace(ch.Message.Content)
		firstLine, _, _ := strings.Cut(content, "\n")
		extra := ""
		if strings.Contains(content, "[Loaded]") {
			extra = " (materialized skill content)"
		}
		fmt.Printf("tool result: %s%s\n", firstLine, extra)
		return
	}
	if ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" {
		fmt.Printf("assistant: %s\n", strings.TrimSpace(ch.Message.Content))
	}
}

func printSkillState(
	ctx context.Context,
	svc session.Service,
	sessionID string,
) {
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}
	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		log.Fatalf("get session: %v", err)
	}

	keys := listSkillStateKeys(sess.State)
	fmt.Println("skill state keys:")
	if len(keys) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, k := range keys {
		v := sess.State[k]
		if len(v) == 0 {
			fmt.Printf("  - %s = <cleared>\n", k)
			continue
		}
		if string(v) == stateValueOne {
			fmt.Printf("  - %s = %s\n", k, stateValueOne)
			continue
		}
		fmt.Printf("  - %s = %s\n", k, string(v))
	}
}

func listSkillStateKeys(state session.StateMap) []string {
	if len(state) == 0 {
		return nil
	}
	var out []string
	for k := range state {
		if strings.HasPrefix(k, skill.StateKeyLoadedPrefix) ||
			strings.HasPrefix(k, skill.StateKeyDocsPrefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

type stepModel struct {
	skill string
	step  int
}

func newStepModel(skillName string) *stepModel {
	return &stepModel{skill: skillName}
}

func (m *stepModel) Info() model.Info {
	return model.Info{Name: "skillloadmode-mock-model"}
}

func (m *stepModel) GenerateContent(
	ctx context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.step++

	var rsp *model.Response
	switch m.step {
	case 1:
		rsp = toolCallResponse(
			"call-1",
			"skill_load",
			[]byte(fmt.Sprintf(`{"skill":%q}`, m.skill)),
		)
	case 2:
		rsp = assistantResponse("loaded and ready")
	default:
		rsp = assistantResponse("done")
	}

	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case ch <- rsp:
		}
	}()
	return ch, nil
}

func toolCallResponse(
	id string,
	toolName string,
	args []byte,
) *model.Response {
	return &model.Response{
		ID:        id,
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   id,
					Function: model.FunctionDefinitionParam{
						Name:      toolName,
						Arguments: args,
					},
				}},
			},
		}},
	}
}

func assistantResponse(content string) *model.Response {
	return &model.Response{
		ID:        "assistant",
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
}
