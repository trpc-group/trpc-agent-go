//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	agentSessionPrefix = "anthropic-skills-agent-"
)

type docCaseAnswer struct {
	Skill        string `json:"skill"`
	FirstHeading string `json:"first_heading"`
}

type toolCall struct {
	ID   string
	Name string
	Args []byte
}

type toolResult struct {
	ToolID  string
	Name    string
	Content string
}

type runCapture struct {
	ToolCalls   []toolCall
	ToolResults []toolResult
	AnswerText  string
	Usage       usageTotals
}

type usageTotals struct {
	Steps             int
	PromptTokens      int
	CachedTokens      int
	CacheCreateTokens int
	CacheReadTokens   int
	CompletionTokens  int
	TotalTokens       int
}

func checkAgentEnv(modelName string) error {
	if strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("empty model name")
	}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return fmt.Errorf("OPENAI_API_KEY is not set")
	}
	return nil
}

func runAgentSuite(
	repo skillrepo.Repository,
	exec codeexecutor.CodeExecutor,
	workRoot string,
	modelName string,
	skipDocCases bool,
	withExec bool,
	onlySkill string,
	debug bool,
	progress bool,
) error {
	agt := newBenchAgent(
		repo,
		exec,
		modelName,
		llmagent.SkillLoadModeTurn,
		false,
	)
	r := runner.NewRunner(defaultAppName, agt)
	defer r.Close()

	summaries := repo.Summaries()
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})

	selected := make([]skillrepo.Summary, 0, len(summaries))
	for _, s := range summaries {
		if onlySkill == "" || s.Name == onlySkill {
			selected = append(selected, s)
		}
	}

	progressf(progress, "🤖 Agent Suite")
	progressf(progress, "  Model: %s", modelName)
	progressf(progress, "  Skills: %d", len(selected))
	if skipDocCases {
		progressf(progress, "  Skipping per-skill doc cases")
	} else {
		for i, s := range selected {
			progressf(
				progress,
				"📌 Skill [%d/%d]: %s",
				i+1,
				len(selected),
				s.Name,
			)
			if err := runAgentDocCase(
				r,
				repo,
				s.Name,
				debug,
				progress,
			); err != nil {
				return err
			}
		}
	}

	if !withExec {
		progressf(progress, "✅ Agent Suite PASS")
		return nil
	}
	if !skipDocCases {
		progressf(progress, "  🧪 Extra Case: internal-comms")
		if err := runInternalCommsSelectDocsCase(
			r,
			repo,
			debug,
			progress,
		); err != nil {
			return err
		}
	}
	if onlySkill == "" {
		progressf(progress, "  🧪 Composed Scenarios")
		if err := runComposedScenarios(
			r,
			repo,
			workRoot,
			debug,
			progress,
		); err != nil {
			return err
		}
	}
	progressf(progress, "✅ Agent Suite PASS")
	return nil
}

const (
	stateValueLoaded  = "1"
	stateValueAllDocs = "*"
)

func runTokenReportSuite(
	repo skillrepo.Repository,
	exec codeexecutor.CodeExecutor,
	workRoot string,
	modelName string,
	allDocs bool,
	debug bool,
	progress bool,
) error {
	agt := newBenchAgent(
		repo,
		exec,
		modelName,
		llmagent.SkillLoadModeSession,
		false,
	)
	svc := inmemory.NewSessionService()
	r := runner.NewRunner(
		defaultAppName,
		agt,
		runner.WithSessionService(svc),
	)
	defer r.Close()

	progressf(progress, "📊 Token Report Suite")
	progressf(progress, "  Model: %s", modelName)
	progressf(progress, "  Scenario: %s", scenarioBrandLanding)
	progressf(progress, "  Full injection docs: %t", allDocs)

	progressf(progress, "  Mode A: progressive disclosure")
	progressiveSession := agentSessionPrefix + "token-a-" +
		scenarioBrandLanding
	a, err := runBrandLandingForTokenReport(
		r,
		repo,
		workRoot,
		progressiveSession,
		debug,
		progress,
	)
	if err != nil {
		return fmt.Errorf("progressive: %w", err)
	}

	progressf(progress, "  Mode B: full injection (all skills)")
	fullSession := agentSessionPrefix + "token-b-" +
		scenarioBrandLanding
	if err := createAllSkillsSession(
		svc,
		fullSession,
		repo,
		allDocs,
	); err != nil {
		return fmt.Errorf("full injection: %w", err)
	}
	b, err := runBrandLandingForTokenReport(
		r,
		repo,
		workRoot,
		fullSession,
		debug,
		progress,
	)
	if err != nil {
		return fmt.Errorf("full injection: %w", err)
	}

	printTokenComparison(a.Usage, b.Usage, progress)
	return nil
}

func createAllSkillsSession(
	svc session.Service,
	sessionID string,
	repo skillrepo.Repository,
	allDocs bool,
) error {
	if svc == nil {
		return fmt.Errorf("nil session service")
	}
	if repo == nil {
		return fmt.Errorf("nil repo")
	}
	state := make(session.StateMap)
	for _, s := range repo.Summaries() {
		state[skillrepo.StateKeyLoadedPrefix+s.Name] = []byte(
			stateValueLoaded,
		)
		if allDocs {
			state[skillrepo.StateKeyDocsPrefix+s.Name] = []byte(
				stateValueAllDocs,
			)
		}
	}
	key := session.Key{
		AppName:   defaultAppName,
		UserID:    defaultUserID,
		SessionID: sessionID,
	}
	_, err := svc.CreateSession(context.Background(), key, state)
	return err
}

func printTokenComparison(a usageTotals, b usageTotals, progress bool) {
	progressf(progress, "  📊 Token Summary (sum across model calls)")
	progressf(
		progress,
		"    A prompt=%d completion=%d total=%d steps=%d",
		a.PromptTokens,
		a.CompletionTokens,
		a.TotalTokens,
		a.Steps,
	)
	progressf(
		progress,
		"    B prompt=%d completion=%d total=%d steps=%d",
		b.PromptTokens,
		b.CompletionTokens,
		b.TotalTokens,
		b.Steps,
	)

	if b.TotalTokens <= 0 || b.PromptTokens <= 0 {
		return
	}
	totalSavings := 100.0 * (1.0 -
		float64(a.TotalTokens)/float64(b.TotalTokens))
	promptSavings := 100.0 * (1.0 -
		float64(a.PromptTokens)/float64(b.PromptTokens))
	progressf(
		progress,
		"    Savings: total=%.2f%% prompt=%.2f%%",
		totalSavings,
		promptSavings,
	)
}

func newBenchAgent(
	repo skillrepo.Repository,
	exec codeexecutor.CodeExecutor,
	modelName string,
	skillLoadMode string,
	loadedInToolResults bool,
) agent.Agent {
	m := openai.New(modelName)

	maxTokens := intPtr(4096)
	stream := false
	gen := model.GenerationConfig{
		MaxTokens: maxTokens,
		Stream:    stream,
	}
	if strings.Contains(strings.ToLower(modelName), "gpt-5") {
		maxTokens = intPtr(8192)
		gen.MaxTokens = maxTokens
		reasoning := "low"
		gen.ReasoningEffort = &reasoning
	} else {
		gen.Temperature = floatPtr(0.0)
	}

	opts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithSkills(repo),
		llmagent.WithSkillLoadMode(skillLoadMode),
		llmagent.WithCodeExecutor(exec),
		llmagent.WithPlanner(react.New()),
		llmagent.WithMaxToolIterations(20),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithInstruction(benchSystemPrompt()),
	}
	if loadedInToolResults {
		opts = append(
			opts,
			llmagent.WithSkillsLoadedContentInToolResults(true),
		)
	}
	return llmagent.New(
		"anthropic-skills-agent",
		opts...,
	)
}

func benchSystemPrompt() string {
	return strings.Join([]string{
		"You are running a benchmark.",
		"You MUST follow user instructions exactly.",
		"Required tool calls are mandatory.",
		"JSON-only rules apply to the final assistant message only.",
	}, "\n")
}

func runAgentDocCase(
	r runner.Runner,
	repo skillrepo.Repository,
	skillName string,
	debug bool,
	progress bool,
) error {
	wantHeading, wantDocs, err := expectedSkillBasics(repo, skillName)
	if err != nil {
		return err
	}
	msg := model.NewUserMessage(docCasePrompt(skillName))

	sessionID := agentSessionPrefix + skillName
	start := time.Now()
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return err
	}
	progressf(
		progress,
		"  ✅ %s done in %s",
		skillName,
		time.Since(start).Truncate(time.Millisecond),
	)
	if !hasSkillToolCall(cap.ToolCalls, toolSkillLoad, skillName) {
		return fmt.Errorf("%s: missing skill_load%s",
			skillName, debugSuffix(cap, debug))
	}
	if !hasSkillToolCall(cap.ToolCalls, toolSkillListDocs, skillName) {
		return fmt.Errorf("%s: missing skill_list_docs%s",
			skillName, debugSuffix(cap, debug))
	}
	if !hasSkillToolCall(cap.ToolCalls, toolSkillRun, skillName) {
		return fmt.Errorf("%s: missing skill_run%s",
			skillName, debugSuffix(cap, debug))
	}
	gotDocs := listDocsCount(cap.ToolResults)
	if gotDocs != wantDocs {
		return fmt.Errorf("%s: doc_count=%d, want %d",
			skillName, gotDocs, wantDocs)
	}
	gotHeading, ok := headingFromLastSkillRun(cap.ToolResults)
	if !ok {
		return fmt.Errorf("%s: missing skill_run stdout%s",
			skillName, debugSuffix(cap, debug))
	}
	if gotHeading != wantHeading {
		return fmt.Errorf("%s: heading mismatch%s",
			skillName, debugSuffix(cap, debug))
	}
	return nil
}

func expectedSkillBasics(
	repo skillrepo.Repository,
	skillName string,
) (string, int, error) {
	if repo == nil {
		return "", 0, fmt.Errorf("nil repo")
	}
	sk, err := repo.Get(skillName)
	if err != nil || sk == nil {
		return "", 0, fmt.Errorf("skill %q not found", skillName)
	}
	return firstHeadingFromFile(repo, skillName), len(sk.Docs), nil
}

func firstHeading(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func firstHeadingFromFile(
	repo skillrepo.Repository,
	skillName string,
) string {
	root, err := repo.Path(skillName)
	if err != nil || strings.TrimSpace(root) == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, skillDefinitionFile))
	if err != nil {
		return ""
	}
	return firstHeading(string(b))
}

func docCasePrompt(skillName string) string {
	lines := []string{
		"Do this exactly:",
		"1) Call skill_load for skill " + jsonQuote(skillName),
		"2) Call skill_list_docs for the same skill",
		"3) Call skill_run to find the first heading line in " +
			skillDefinitionFile,
		"   (first line whose trimmed line starts with '#')",
		"4) Return ONLY JSON:",
		`{"skill":` + jsonQuote(skillName) + `,"ok":true}`,
		"",
		"For skill_run, run a simple shell pipeline such as:",
		`grep -E '^[[:space:]]*#' ` + skillDefinitionFile +
			` | head -n 1`,
	}
	return strings.Join(lines, "\n")
}

func runInternalCommsSelectDocsCase(
	r runner.Runner,
	repo skillrepo.Repository,
	debug bool,
	progress bool,
) error {
	wantLines, err := expected3PFormatLines(repo)
	if err != nil {
		return err
	}
	msg := model.NewUserMessage(internalCommsPrompt())
	sessionID := agentSessionPrefix + skillInternalComms + "-docs"
	start := time.Now()
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return err
	}
	progressf(
		progress,
		"  ✅ internal-comms done in %s",
		time.Since(start).Truncate(time.Millisecond),
	)
	if !hasSkillToolCall(cap.ToolCalls, toolSkillLoad,
		skillInternalComms) {
		return fmt.Errorf("internal-comms: missing skill_load%s",
			debugSuffix(cap, debug))
	}
	if !hasSelectDocsCall(cap.ToolCalls, skillInternalComms,
		"examples/3p-updates.md") {
		return fmt.Errorf("internal-comms: missing skill_select_docs%s",
			debugSuffix(cap, debug))
	}
	if !hasSkillToolCall(cap.ToolCalls, toolSkillRun,
		skillInternalComms) {
		return fmt.Errorf("internal-comms: missing skill_run%s",
			debugSuffix(cap, debug))
	}
	gotLines, ok := formatLinesFromLastSkillRun(cap.ToolResults)
	if !ok {
		return fmt.Errorf("internal-comms: missing skill_run stdout%s",
			debugSuffix(cap, debug))
	}
	if len(gotLines) != len(wantLines) {
		return fmt.Errorf("internal-comms: wrong line count%s",
			debugSuffix(cap, debug))
	}
	for i := range wantLines {
		if strings.TrimSpace(gotLines[i]) != wantLines[i] {
			return fmt.Errorf("internal-comms: line %d mismatch%s",
				i, debugSuffix(cap, debug))
		}
	}
	return nil
}

func debugSuffix(cap runCapture, debug bool) string {
	if !debug {
		return ""
	}
	return fmt.Sprintf(" tool_calls=%s answer=%q",
		strings.Join(toolCallNames(cap.ToolCalls), ","),
		strings.TrimSpace(cap.AnswerText),
	)
}

func toolCallNames(calls []toolCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Name)
	}
	return out
}

func requireSkillLoad(
	cap runCapture,
	skillName string,
	debug bool,
) error {
	if hasSkillToolCall(cap.ToolCalls, toolSkillLoad, skillName) {
		return nil
	}
	return fmt.Errorf("missing skill_load %q%s",
		skillName, debugSuffix(cap, debug))
}

func requireSuccessfulSkillRun(
	cap runCapture,
	skillName string,
	debug bool,
) error {
	var (
		found bool
		last  error
	)
	for _, c := range cap.ToolCalls {
		if c.Name != toolSkillRun {
			continue
		}
		var in skillRunArgs
		if err := json.Unmarshal(c.Args, &in); err != nil {
			continue
		}
		if in.Skill != skillName {
			continue
		}
		found = true
		res, ok := toolResultForCall(
			cap.ToolResults,
			runToolCall{ID: c.ID},
		)
		if !ok {
			last = fmt.Errorf("missing tool result for skill_run %q",
				skillName)
			continue
		}
		var rr skillRunResult
		if err := json.Unmarshal([]byte(res.Content), &rr); err != nil {
			last = fmt.Errorf("invalid skill_run result for %q: %w",
				skillName, err)
			continue
		}
		if rr.ExitCode == 0 && !rr.TimedOut {
			return nil
		}
		last = fmt.Errorf("skill_run failed for %q: exit=%d timeout=%t",
			skillName, rr.ExitCode, rr.TimedOut)
	}
	if !found {
		return fmt.Errorf("missing skill_run %q%s",
			skillName, debugSuffix(cap, debug))
	}
	if last != nil {
		if debug {
			return fmt.Errorf("%v%s", last, debugSuffix(cap, debug))
		}
		return last
	}
	return fmt.Errorf("missing skill_run result %q%s",
		skillName, debugSuffix(cap, debug))
}

func headingFromLastSkillRun(results []toolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Name != toolSkillRun {
			continue
		}
		var rr skillRunResult
		if err := json.Unmarshal([]byte(results[i].Content), &rr); err != nil {
			continue
		}
		h := headingFromStdout(rr.Stdout)
		if h != "" {
			return h, true
		}
	}
	return "", false
}

func headingFromStdout(stdout string) string {
	for _, line := range strings.Split(stdout, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		i := strings.IndexByte(s, '#')
		if i < 0 {
			continue
		}
		return strings.TrimSpace(s[i:])
	}
	return ""
}

func formatLinesFromLastSkillRun(results []toolResult) ([]string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Name != toolSkillRun {
			continue
		}
		var rr skillRunResult
		if err := json.Unmarshal([]byte(results[i].Content), &rr); err != nil {
			continue
		}
		var out []string
		for _, line := range strings.Split(rr.Stdout, "\n") {
			s := strings.TrimSpace(line)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		if len(out) >= 4 {
			return out[:4], true
		}
		return out, len(out) > 0
	}
	return nil, false
}

func internalCommsPrompt() string {
	doc := "examples/3p-updates.md"
	awkCmd := "awk '/\\\\[pick an emoji\\\\]/{print; " +
		"getline; print; getline; print; getline; print; exit}' " +
		doc
	lines := []string{
		"Use the internal-comms skill.",
		"1) Call skill_load for " + jsonQuote(skillInternalComms),
		"2) Call skill_select_docs with mode=replace and docs=" +
			"[" + jsonQuote(doc) + "]",
		"3) Call skill_run to print the 4-line Formatting template:",
		awkCmd,
		"4) Return ONLY JSON: " +
			`{"skill":` + jsonQuote(skillInternalComms) +
			`,"ok":true}`,
	}
	return strings.Join(lines, "\n")
}

func expected3PFormatLines(repo skillrepo.Repository) ([]string, error) {
	if repo == nil {
		return nil, fmt.Errorf("nil repo")
	}
	sk, err := repo.Get(skillInternalComms)
	if err != nil || sk == nil {
		return nil, fmt.Errorf("internal-comms not found")
	}
	var doc string
	for _, d := range sk.Docs {
		if d.Path == "examples/3p-updates.md" {
			doc = d.Content
			break
		}
	}
	if strings.TrimSpace(doc) == "" {
		return nil, fmt.Errorf("missing examples/3p-updates.md")
	}
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if strings.Contains(lines[i], "[pick an emoji]") {
			if i+3 < len(lines) {
				out := []string{
					strings.TrimSpace(lines[i]),
					strings.TrimSpace(lines[i+1]),
					strings.TrimSpace(lines[i+2]),
					strings.TrimSpace(lines[i+3]),
				}
				return out, nil
			}
			break
		}
	}
	return nil, fmt.Errorf("format template not found")
}

func runOnce(
	r runner.Runner,
	userID string,
	sessionID string,
	msg model.Message,
	debug bool,
	progress bool,
) (runCapture, error) {
	events, err := r.Run(context.Background(), userID, sessionID, msg)
	if err != nil {
		return runCapture{}, err
	}
	pr := newAgentPrinter(progress, debug)
	return collect(events, pr)
}

type agentPrinter struct {
	enabled bool
	debug   bool

	step  int
	tools int

	toolIDToName map[string]string
}

func newAgentPrinter(enabled bool, debug bool) *agentPrinter {
	return &agentPrinter{
		enabled:      enabled,
		debug:        debug,
		toolIDToName: make(map[string]string),
	}
}

func (p *agentPrinter) event(ev *event.Event) {
	if !p.enabled || ev == nil {
		return
	}
	if ev.Error != nil {
		progressf(p.enabled, "  event error: %s",
			strings.TrimSpace(ev.Error.Message))
	}
	if ev.Response == nil {
		return
	}
	if p.debug && ev.Response.Object != "" {
		progressf(p.enabled, "  event object: %s", ev.Response.Object)
	}
	if ev.Response.Usage != nil {
		p.step++
		p.printUsage(*ev.Response.Usage)
	}
	for _, ch := range ev.Response.Choices {
		p.message(ch.Message)
		p.message(ch.Delta)
	}
}

func (p *agentPrinter) printUsage(u model.Usage) {
	progressf(
		p.enabled,
		"  📊 Step %d | Tokens: %d (in:%d out:%d)",
		p.step,
		u.TotalTokens,
		u.PromptTokens,
		u.CompletionTokens,
	)
}

func (p *agentPrinter) message(m model.Message) {
	if !p.enabled {
		return
	}
	if len(m.ToolCalls) > 0 {
		p.toolCalls(m.ToolCalls)
		return
	}
	if m.Role == model.RoleTool && strings.TrimSpace(m.Content) != "" {
		p.toolResult(m)
		return
	}
	if m.Role == model.RoleAssistant &&
		strings.TrimSpace(m.Content) != "" &&
		len(m.ToolCalls) == 0 {
		p.assistantText(m.Content)
		return
	}
	if p.debug && strings.TrimSpace(m.Content) != "" {
		progressf(p.enabled, "  %s:", m.Role)
		p.printBlock("    ", m.Content, defaultPreviewChars)
	}
}

func (p *agentPrinter) toolCalls(calls []model.ToolCall) {
	progressf(p.enabled, "  🔧 Tool Calls: %d", len(calls))
	p.tools += len(calls)
	for i, c := range calls {
		name := c.Function.Name
		progressf(p.enabled, "    [%d] %s", i+1, name)
		if c.ID != "" {
			p.toolIDToName[c.ID] = name
		}

		argText := strings.TrimSpace(string(c.Function.Arguments))
		if argText == "" {
			continue
		}
		progressf(p.enabled, "        Args:")
		p.printBlock("          ", argText, defaultPreviewChars)
		if p.debug && c.ID != "" {
			progressf(p.enabled, "      tool_id: %s", c.ID)
		}
	}
}

func (p *agentPrinter) toolResult(m model.Message) {
	name := m.ToolName
	if name == "" && m.ToolID != "" {
		name = p.toolIDToName[m.ToolID]
	}
	if name == "" {
		name = "unknown"
	}
	progressf(p.enabled, "  ✅ Tool Result [%s]:", name)
	p.printBlock("    ", m.Content, defaultPreviewChars)
	if p.debug && m.ToolID != "" {
		progressf(p.enabled, "    tool_id: %s", m.ToolID)
	}
}

func (p *agentPrinter) assistantText(content string) {
	progressf(p.enabled, "  💭 Agent Response:")
	p.printBlock("    ", content, defaultPreviewChars)
}

func (p *agentPrinter) printBlock(
	prefix string,
	content string,
	maxChars int,
) {
	printTextBlock(
		p.enabled,
		p.debug,
		prefix,
		content,
		maxChars,
	)
}

func collect(
	events <-chan *event.Event,
	pr *agentPrinter,
) (runCapture, error) {
	var out runCapture
	var runErr error
	seenUsage := make(map[string]struct{})
	for ev := range events {
		if ev == nil || ev.Response == nil {
			if ev != nil {
				pr.event(ev)
			}
			continue
		}
		pr.event(ev)
		if ev.Error != nil && runErr == nil {
			runErr = fmt.Errorf("%s: %s",
				ev.Error.Type,
				strings.TrimSpace(ev.Error.Message),
			)
		}
		accumulateUsage(&out.Usage, ev.Response, seenUsage)
		for _, ch := range ev.Choices {
			out = mergeChoice(out, ch.Message)
			out = mergeChoice(out, ch.Delta)
		}
	}
	out.AnswerText = strings.TrimSpace(out.AnswerText)
	if pr != nil && pr.enabled {
		progressf(
			pr.enabled,
			"  ✅ Final response | Steps: %d, Tool calls: %d",
			pr.step,
			pr.tools,
		)
	}
	if runErr != nil {
		return out, runErr
	}
	return out, nil
}

func accumulateUsage(
	totals *usageTotals,
	resp *model.Response,
	seen map[string]struct{},
) {
	if totals == nil || resp == nil || resp.Usage == nil {
		return
	}
	id := strings.TrimSpace(resp.ID)
	if id != "" {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
	}
	totals.Steps++
	totals.PromptTokens += resp.Usage.PromptTokens
	totals.CachedTokens += resp.Usage.PromptTokensDetails.CachedTokens
	totals.CacheCreateTokens +=
		resp.Usage.PromptTokensDetails.CacheCreationTokens
	totals.CacheReadTokens += resp.Usage.PromptTokensDetails.CacheReadTokens
	totals.CompletionTokens += resp.Usage.CompletionTokens
	totals.TotalTokens += resp.Usage.TotalTokens
}

func mergeChoice(out runCapture, m model.Message) runCapture {
	if m.Content != "" && m.Role == model.RoleAssistant {
		out.AnswerText = m.Content
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, toolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}
	if m.ToolName != "" && m.Content != "" {
		out.ToolResults = append(out.ToolResults, toolResult{
			ToolID:  m.ToolID,
			Name:    m.ToolName,
			Content: m.Content,
		})
	}
	return out
}

func hasSkillToolCall(
	calls []toolCall,
	toolName string,
	skillName string,
) bool {
	for _, c := range calls {
		if c.Name != toolName {
			continue
		}
		var in struct {
			Skill string `json:"skill"`
		}
		if err := json.Unmarshal(c.Args, &in); err != nil {
			continue
		}
		if in.Skill == skillName {
			return true
		}
	}
	return false
}

func hasSelectDocsCall(
	calls []toolCall,
	skillName string,
	docName string,
) bool {
	for _, c := range calls {
		if c.Name != toolSkillSelectDoc {
			continue
		}
		var in struct {
			Skill string   `json:"skill"`
			Docs  []string `json:"docs"`
			Mode  string   `json:"mode"`
		}
		if err := json.Unmarshal(c.Args, &in); err != nil {
			continue
		}
		if in.Skill != skillName || in.Mode != "replace" {
			continue
		}
		if len(in.Docs) == 1 && in.Docs[0] == docName {
			return true
		}
	}
	return false
}

type runToolCall struct {
	ID      string
	Command string
}

func runComposedScenarios(
	r runner.Runner,
	repo skillrepo.Repository,
	workRoot string,
	debug bool,
	progress bool,
) error {
	if err := runScenarioBrandLanding(
		r,
		repo,
		workRoot,
		debug,
		progress,
	); err != nil {
		return err
	}
	if err := runScenarioLaunchKit(
		r,
		repo,
		workRoot,
		debug,
		progress,
	); err != nil {
		return err
	}
	if err := runScenarioDocxToPPTX(
		r,
		workRoot,
		debug,
		progress,
	); err != nil {
		return err
	}
	if err := runScenarioTemplateToValidate(
		r,
		workRoot,
		debug,
		progress,
	); err != nil {
		return err
	}
	return nil
}

func runScenarioBrandLanding(
	r runner.Runner,
	repo skillrepo.Repository,
	workRoot string,
	debug bool,
	progress bool,
) error {
	sessionID := agentSessionPrefix + "scenario-" + scenarioBrandLanding
	msg := model.NewUserMessage(brandLandingPrompt())
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return err
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillBrandGuide,
		debug,
	); err != nil {
		return fmt.Errorf("scenario brand-landing: %w", err)
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillFrontendDesign,
		debug,
	); err != nil {
		return fmt.Errorf("scenario brand-landing: %w", err)
	}
	wsDir, err := latestWorkspaceDir(workRoot, sessionID)
	if err != nil {
		return fmt.Errorf("scenario brand-landing: %w", err)
	}
	if err := verifyBrandLandingOutputs(repo, wsDir); err != nil {
		return fmt.Errorf("scenario brand-landing: %w", err)
	}
	return nil
}

func runBrandLandingForTokenReport(
	r runner.Runner,
	repo skillrepo.Repository,
	workRoot string,
	sessionID string,
	debug bool,
	progress bool,
) (runCapture, error) {
	msg := model.NewUserMessage(brandLandingPrompt())
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return cap, err
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillBrandGuide,
		debug,
	); err != nil {
		return cap, err
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillFrontendDesign,
		debug,
	); err != nil {
		return cap, err
	}
	wsDir, err := latestWorkspaceDir(workRoot, sessionID)
	if err != nil {
		return cap, err
	}
	if err := verifyBrandLandingOutputs(repo, wsDir); err != nil {
		return cap, err
	}
	return cap, nil
}

func runScenarioLaunchKit(
	r runner.Runner,
	repo skillrepo.Repository,
	workRoot string,
	debug bool,
	progress bool,
) error {
	sessionID := agentSessionPrefix + "scenario-" + scenarioLaunchKit
	msg := model.NewUserMessage(launchKitPrompt())
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return err
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillThemeFactory,
		debug,
	); err != nil {
		return fmt.Errorf("scenario launch-kit: %w", err)
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillSlackGIF,
		debug,
	); err != nil {
		return fmt.Errorf("scenario launch-kit: %w", err)
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillPPTX,
		debug,
	); err != nil {
		return fmt.Errorf("scenario launch-kit: %w", err)
	}
	wsDir, err := latestWorkspaceDir(workRoot, sessionID)
	if err != nil {
		return fmt.Errorf("scenario launch-kit: %w", err)
	}
	if err := verifyLaunchKitOutputs(repo, wsDir); err != nil {
		return fmt.Errorf("scenario launch-kit: %w", err)
	}
	return nil
}

func runScenarioDocxToPPTX(
	r runner.Runner,
	workRoot string,
	debug bool,
	progress bool,
) error {
	sessionID := agentSessionPrefix + "scenario-" + scenarioDocxToPPTX
	msg := model.NewUserMessage(docxOOXMLPrompt())
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return err
	}
	if err := requireSuccessfulSkillRun(cap, skillDocx, debug); err != nil {
		return fmt.Errorf("scenario docx: %w", err)
	}
	if err := requireSuccessfulSkillRun(cap, skillPPTX, debug); err != nil {
		return fmt.Errorf("scenario docx: %w", err)
	}
	wsDir, err := latestWorkspaceDir(workRoot, sessionID)
	if err != nil {
		return fmt.Errorf("scenario docx: %w", err)
	}
	if err := verifyDocxOOXMLOutputs(wsDir); err != nil {
		return fmt.Errorf("scenario docx: %w", err)
	}
	return nil
}

func runScenarioTemplateToValidate(
	r runner.Runner,
	workRoot string,
	debug bool,
	progress bool,
) error {
	sessionID := agentSessionPrefix + "scenario-" +
		scenarioTemplateToValidate
	msg := model.NewUserMessage(templateToValidatePrompt())
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	if err != nil {
		return err
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillTemplate,
		debug,
	); err != nil {
		return fmt.Errorf("scenario template: %w", err)
	}
	if err := requireSuccessfulSkillRun(
		cap,
		skillCreator,
		debug,
	); err != nil {
		return fmt.Errorf("scenario template: %w", err)
	}
	wsDir, err := latestWorkspaceDir(workRoot, sessionID)
	if err != nil {
		return fmt.Errorf("scenario template: %w", err)
	}
	if err := verifyTemplateOutputs(wsDir); err != nil {
		return fmt.Errorf("scenario template: %w", err)
	}
	return nil
}

func assertSkillRun(
	cap runCapture,
	skillName string,
	commandMustContain string,
	stdoutMustContain string,
) error {
	call, ok := findSkillRunCall(
		cap.ToolCalls,
		skillName,
		commandMustContain,
	)
	if !ok {
		return fmt.Errorf("missing skill_run for %q", skillName)
	}
	res, ok := toolResultForCall(cap.ToolResults, call)
	if !ok {
		return fmt.Errorf("missing tool result for skill_run %q", skillName)
	}
	var rr skillRunResult
	if err := json.Unmarshal([]byte(res.Content), &rr); err != nil {
		return fmt.Errorf("invalid skill_run result: %w", err)
	}
	if rr.ExitCode != 0 || rr.TimedOut {
		return fmt.Errorf("bad skill_run result: exit=%d timeout=%t",
			rr.ExitCode, rr.TimedOut)
	}
	if !strings.Contains(rr.Stdout, stdoutMustContain) {
		return fmt.Errorf("missing stdout marker %q", stdoutMustContain)
	}
	return nil
}

func findSkillRunCall(
	calls []toolCall,
	skillName string,
	commandMustContain string,
) (runToolCall, bool) {
	for _, c := range calls {
		if c.Name != toolSkillRun {
			continue
		}
		var in skillRunArgs
		if err := json.Unmarshal(c.Args, &in); err != nil {
			continue
		}
		if in.Skill != skillName {
			continue
		}
		if strings.TrimSpace(commandMustContain) != "" &&
			!strings.Contains(in.Command, commandMustContain) {
			continue
		}
		return runToolCall{
			ID:      c.ID,
			Command: in.Command,
		}, true
	}
	return runToolCall{}, false
}

func toolResultForCall(
	results []toolResult,
	call runToolCall,
) (toolResult, bool) {
	if strings.TrimSpace(call.ID) != "" {
		for _, r := range results {
			if r.ToolID == call.ID && r.Name == toolSkillRun {
				return r, true
			}
		}
	}
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Name == toolSkillRun {
			return results[i], true
		}
	}
	return toolResult{}, false
}

func launchKitPrompt() string {
	return strings.Join([]string{
		"I need a tiny one-slide launch kit deck.",
		"",
		"Please use these skills:",
		"- theme-factory (extract the Ocean Depths theme Teal color)",
		"- slack-gif-creator (generate an emoji-style GIF)",
		"- pptx (build the PPTX and embed the GIF)",
		"",
		"Deliverables (exact paths):",
		"- " + fileLaunchThemeSpecJSON,
		"  JSON with fields: theme, teal",
		"  Example: {\"theme\":\"ocean-depths\",\"teal\":\"#2D8B8B\"}",
		"- " + fileLaunchGIF,
		"  A valid GIF file (64x64 is fine).",
		"- " + fileLaunchPPTX,
		"  A PPTX with ONE slide:",
		"  - title text: " + jsonQuote(launchKitSlideTitle),
		"  - title color: use the Teal from the JSON spec",
		"  - embed the GIF on the slide",
		"",
		"Constraints:",
		"- Keep all files inside the skill workspace (use out/ and work/).",
		"- If you need Python deps, create a per-skill .venv and install.",
		"- Do not substitute other skills for these tasks.",
		"",
		"When done, reply with ONLY JSON:",
		`{"scenario":"` + scenarioLaunchKit + `","ok":true}`,
	}, "\n")
}

func docxOOXMLPrompt() string {
	return strings.Join([]string{
		"I need a tiny meeting-notes DOCX plus an OOXML sanity check.",
		"",
		"Please use these skills:",
		"- docx (create the DOCX; do NOT build OOXML by hand)",
		"- pptx (unpack and inspect the DOCX)",
		"",
		"Requirements:",
		"1) Create the DOCX at " + fileDocxBench +
			" using the docx skill",
		"   The DOCX must contain the exact text: " + jsonQuote(markerDocxOK),
		"   Use Python 3 + python-docx in a local .venv (avoid npm/pandoc).",
		"2) Unpack it to " + dirUnpackedDocx + " using the pptx skill",
		"   Use Python's zipfile module (avoid extra deps).",
		"3) Make sure the marker text is present in the unpacked XML.",
		"",
		"When done, reply with ONLY JSON:",
		`{"scenario":"` + scenarioDocxToPPTX + `","ok":true}`,
	}, "\n")
}

func templateToValidatePrompt() string {
	benchName := "bench-template-skill"
	benchDesc := "Minimal benchmark skill for trpc-agent-go."
	return strings.Join([]string{
		"I want to create a new skill based on the template.",
		"",
		"Please use these skills:",
		"- template-skill (as the source template)",
		"- skill-creator (to validate the generated skill)",
		"",
		"Requirements:",
		"1) Create " + fileBenchTemplate,
		"   Keep the template content, but make the YAML frontmatter valid.",
		"   Set:",
		"   - name: " + benchName,
		"   - description: " + benchDesc,
		"2) Validate " + dirBenchTemplate +
			" using skill-creator's scripts/quick_validate.py",
		"",
		"When done, reply with ONLY JSON:",
		`{"scenario":"` + scenarioTemplateToValidate + `","ok":true}`,
	}, "\n")
}

func brandLandingPrompt() string {
	return strings.Join([]string{
		"I need a simple static landing page for an internal demo.",
		"",
		"Please use these skills:",
		"- brand-guidelines",
		"- frontend-design",
		"",
		"Deliverables (exact paths):",
		"- " + fileBrandTokens,
		"  JSON object with keys:",
		"  dark, light, mid_gray, light_gray, orange, blue, green",
		"- " + fileBrandStylesCSS,
		"- " + fileBrandIndexHTML,
		"",
		"Do this exactly (order matters):",
		"1) Call skill_load for " + jsonQuote(skillBrandGuide),
		"2) Call skill_run for the same skill: cat SKILL.md",
		"   Find the official hex codes for Dark/Light/Mid Gray/",
		"   Light Gray/Orange/Blue/Green.",
		"3) Call skill_run for the same skill to create:",
		"   - " + fileBrandTokens,
		"   - " + fileBrandStylesCSS,
		"4) Call skill_load for " + jsonQuote(skillFrontendDesign),
		"5) Call skill_run for the same skill to create:",
		"   - " + fileBrandIndexHTML,
		"",
		"Requirements:",
		"- The page title and H1 must be: Bench Landing",
		"- Extract the official hex codes from SKILL.md (do not invent).",
		"- " + fileBrandStylesCSS +
			" must define :root CSS variables with literal",
		"  hex values (no JS token loading).",
		"- " + fileBrandIndexHTML +
			" must link styles.css and use the variables.",
		"- Do NOT modify " + fileBrandTokens + " in frontend-design.",
		"- Use the brand orange as the primary CTA background.",
		"- Keep it accessible (semantic HTML, readable contrast).",
		"- Keep all files inside the skill workspace (use out/).",
		"- If you need Python deps, create a per-skill .venv and install.",
		"",
		"Important:",
		"- For skill_run, pass only a valid shell command string.",
		"  Do not embed JSON/YAML like 'timeout: 120' inside the command.",
		"",
		"When done, reply with ONLY JSON:",
		`{"scenario":"` + scenarioBrandLanding + `","ok":true}`,
	}, "\n")
}

func listDocsCount(results []toolResult) int {
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Name != toolSkillListDocs {
			continue
		}
		var arr []string
		if err := json.Unmarshal([]byte(results[i].Content), &arr); err != nil {
			continue
		}
		return len(arr)
	}
	return -1
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
