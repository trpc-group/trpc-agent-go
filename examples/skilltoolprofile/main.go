//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the SkillToolProfile option.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName        = "skilltoolprofile-example"
	agentName      = "skilltoolprofile-agent"
	userID         = "skilltoolprofile-user"
	defaultProfile = llmagent.SkillToolProfileFull
	demoSkillName  = "demo-profile"
)

func main() {
	var (
		flagProfile = flag.String(
			"profile",
			string(defaultProfile),
			"Skill tool profile: full|knowledge_only",
		)
		flagSkillsRoot = flag.String(
			"skills-root",
			defaultSkillsRoot(),
			"Skills root directory",
		)
	)
	flag.Parse()

	profile, err := parseProfile(*flagProfile)
	if err != nil {
		log.Fatal(err)
	}

	repo, err := skill.NewFSRepository(*flagSkillsRoot)
	if err != nil {
		log.Fatalf("load skills repo: %v", err)
	}

	agt, executorLabel := newProfileAgent(profile, repo)
	r := runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := fmt.Sprintf(
		"skilltoolprofile-%d",
		time.Now().Unix(),
	)

	fmt.Printf("Profile: %s\n", profile)
	fmt.Printf("Skills root: %s\n", *flagSkillsRoot)
	fmt.Printf("Executor: %s\n", executorLabel)
	fmt.Println("Registered skill tools:")
	for _, name := range listSkillToolNames(agt.Tools()) {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Println()

	if profile == llmagent.SkillToolProfileKnowledgeOnly {
		fmt.Println(
			"Demo flow: skill_load -> skill_list_docs -> " +
				"skill_select_docs -> assistant",
		)
	} else {
		fmt.Println("Demo flow: skill_load -> skill_run -> assistant")
	}
	fmt.Printf("Session: %s\n\n", sessionID)

	ctx := context.Background()
	evCh, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("demo"))
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	for ev := range evCh {
		printEvent(ev)
	}
}

func defaultSkillsRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "./skills"
	}
	return filepath.Join(filepath.Dir(filename), "skills")
}

func parseProfile(raw string) (llmagent.SkillToolProfile, error) {
	switch llmagent.SkillToolProfile(strings.ToLower(strings.TrimSpace(raw))) {
	case llmagent.SkillToolProfileFull:
		return llmagent.SkillToolProfileFull, nil
	case llmagent.SkillToolProfileKnowledgeOnly:
		return llmagent.SkillToolProfileKnowledgeOnly, nil
	default:
		return "", fmt.Errorf(
			"invalid -profile %q: want full|knowledge_only",
			raw,
		)
	}
}

func newProfileAgent(
	profile llmagent.SkillToolProfile,
	repo skill.Repository,
) (*llmagent.LLMAgent, string) {
	opts := []llmagent.Option{
		llmagent.WithModel(newProfileModel(profile)),
		llmagent.WithDescription(agentDescription(profile)),
		llmagent.WithInstruction(agentInstruction(profile)),
		llmagent.WithSkills(repo),
		llmagent.WithSkillToolProfile(profile),
	}

	executorLabel := "disabled"
	if profile == llmagent.SkillToolProfileFull {
		opts = append(
			opts,
			llmagent.WithCodeExecutor(localexec.New()),
			// The CodeExecutor here exists so skill_run /
			// workspace_exec can run; it is not a license to
			// auto-execute fenced code from assistant replies. Keep
			// that orthogonal switch explicitly off so the demo
			// showcases only the skill-tool execution path.
			llmagent.WithEnableCodeExecutionResponseProcessor(false),
		)
		executorLabel = "local"
	}

	return llmagent.New(agentName, opts...), executorLabel
}

func listSkillToolNames(ts []tool.Tool) []string {
	names := make([]string, 0)
	for _, tl := range ts {
		decl := tl.Declaration()
		if decl == nil || !strings.HasPrefix(decl.Name, "skill_") {
			continue
		}
		names = append(names, decl.Name)
	}
	sort.Strings(names)
	return names
}

func printEvent(evt *event.Event) {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}

	ch := evt.Response.Choices[0]
	if len(ch.Message.ToolCalls) > 0 {
		fmt.Println("tool calls:")
		for _, tc := range ch.Message.ToolCalls {
			fmt.Printf(
				"  - %s args=%s\n",
				tc.Function.Name,
				string(tc.Function.Arguments),
			)
		}
		return
	}

	if ch.Message.Role == model.RoleTool && ch.Message.Content != "" {
		fmt.Printf("tool result: %s\n", compactText(ch.Message.Content))
		return
	}

	if ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" {
		fmt.Printf("assistant: %s\n", compactText(ch.Message.Content))
	}
}

func compactText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	const max = 180
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func agentDescription(profile llmagent.SkillToolProfile) string {
	if profile == llmagent.SkillToolProfileKnowledgeOnly {
		return "Demonstrates progressive-disclosure skills without command execution."
	}
	return "Demonstrates full skills including skill_run execution."
}

func agentInstruction(profile llmagent.SkillToolProfile) string {
	if profile == llmagent.SkillToolProfileKnowledgeOnly {
		return `You are demonstrating the knowledge_only SkillToolProfile.

Use the skill named "demo-profile".
1. Call skill_load for "demo-profile".
2. Call skill_list_docs for "demo-profile".
3. Call skill_select_docs with docs ["docs/usage.md"].
4. Then explain briefly that this profile loads skill knowledge only and
   does not register skill_run.`
	}

	return `You are demonstrating the full SkillToolProfile.

Use the skill named "demo-profile".
1. Call skill_load for "demo-profile".
2. Call skill_run with:
   - skill: "demo-profile"
   - command: "sh scripts/write_profile.sh out/profile.txt"
   - output_files: ["out/profile.txt"]
3. Then confirm briefly that the skill executed successfully.`
}

type profileModel struct {
	profile llmagent.SkillToolProfile
	step    int
}

func newProfileModel(profile llmagent.SkillToolProfile) *profileModel {
	return &profileModel{profile: profile}
}

func (m *profileModel) Info() model.Info {
	return model.Info{Name: "skilltoolprofile-mock-model"}
}

func (m *profileModel) GenerateContent(
	ctx context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.step++

	var rsp *model.Response
	switch m.profile {
	case llmagent.SkillToolProfileKnowledgeOnly:
		rsp = m.knowledgeOnlyResponse()
	default:
		rsp = m.fullResponse()
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

func (m *profileModel) fullResponse() *model.Response {
	switch m.step {
	case 1:
		return toolCallResponse(
			"call-load",
			"skill_load",
			fmt.Sprintf(`{"skill":%q}`, demoSkillName),
		)
	case 2:
		return toolCallResponse(
			"call-run",
			"skill_run",
			fmt.Sprintf(`{"skill":%q,"command":"sh scripts/write_profile.sh out/profile.txt","output_files":["out/profile.txt"]}`, demoSkillName),
		)
	default:
		return assistantResponse(
			"Full profile complete: the skill was loaded and executed.",
		)
	}
}

func (m *profileModel) knowledgeOnlyResponse() *model.Response {
	switch m.step {
	case 1:
		return toolCallResponse(
			"call-load",
			"skill_load",
			fmt.Sprintf(`{"skill":%q}`, demoSkillName),
		)
	case 2:
		return toolCallResponse(
			"call-list-docs",
			"skill_list_docs",
			fmt.Sprintf(`{"skill":%q}`, demoSkillName),
		)
	case 3:
		return toolCallResponse(
			"call-select-docs",
			"skill_select_docs",
			fmt.Sprintf(`{"skill":%q,"docs":["docs/usage.md"],"mode":"replace"}`, demoSkillName),
		)
	default:
		return assistantResponse(
			"Knowledge-only profile complete: the skill instructions and docs were loaded, but skill_run is unavailable in this profile.",
		)
	}
}

func toolCallResponse(id string, toolName string, args string) *model.Response {
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
						Arguments: []byte(args),
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
