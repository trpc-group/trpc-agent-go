//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	skillsOverviewHeader = "Available skills:"

	skillsCapabilityHeader = "Skill tool availability:"

	skillsToolingGuidanceHeader = "Tooling and workspace guidance:"

	// SkillLoadModeOnce injects loaded skill content for the next model
	// request, then offloads it from session state.
	SkillLoadModeOnce = "once"
	// SkillLoadModeTurn keeps loaded skill content available for all model
	// requests within the current invocation, and offloads it when the next
	// invocation begins.
	SkillLoadModeTurn = "turn"
	// SkillLoadModeSession keeps loaded skill content available across
	// invocations until cleared or the session expires.
	SkillLoadModeSession = "session"

	defaultSkillLoadMode = SkillLoadModeTurn
)

type skillsRequestProcessorOptions struct {
	toolingGuidance   *string
	loadMode          string
	toolResultMode    bool
	maxLoadedSkills   int
	toolProfile       string
	toolFlags         skillprofile.Flags
	toolFlagsResolver func(*agent.Invocation) skillprofile.Flags
	hasToolFlags      bool
	execToolsDisabled bool
	repoResolver      func(*agent.Invocation) skill.Repository
}

// SkillsRequestProcessorOption configures SkillsRequestProcessor.
type SkillsRequestProcessorOption func(*skillsRequestProcessorOptions)

// WithSkillLoadMode sets how long loaded skill bodies/docs remain
// available in the system prompt.
//
// Supported modes:
//   - SkillLoadModeTurn (default)
//   - SkillLoadModeOnce
//   - SkillLoadModeSession (legacy)
func WithSkillLoadMode(mode string) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.loadMode = mode
	}
}

// WithSkillsToolingGuidance overrides the tooling/workspace guidance
// block appended to the skills overview.
//
// Behavior:
//   - Not configured: use the built-in default guidance.
//   - Configured with empty string: omit both the tooling/workspace
//     guidance block and the capability disclosure block.
//   - Configured with non-empty string: append the provided text.
func WithSkillsToolingGuidance(
	guidance string,
) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		text := guidance
		o.toolingGuidance = &text
	}
}

// WithSkillsLoadedContentInToolResults enables an alternative injection
// mode where loaded SKILL.md bodies and selected docs are materialized
// into the corresponding tool result messages
// (skill_load / skill_select_docs) instead of being appended to the
// system prompt.
//
// This keeps the system prompt more stable for prompt caching while
// preserving the progressive disclosure behavior.
func WithSkillsLoadedContentInToolResults(
	enable bool,
) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.toolResultMode = enable
	}
}

// WithSkillToolProfile configures the registered skill tool profile so the
// processor can emit mode-appropriate guidance.
func WithSkillToolProfile(profile string) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.toolProfile = profile
	}
}

// WithSkillToolFlags configures the exact built-in skill tool capabilities so
// the processor can emit guidance that matches the final registered tool set.
func WithSkillToolFlags(flags skillprofile.Flags) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.toolFlags = flags
		o.hasToolFlags = true
	}
}

// WithSkillToolFlagsResolver sets an invocation-aware skill tool capability resolver.
func WithSkillToolFlagsResolver(
	resolver func(*agent.Invocation) skillprofile.Flags,
) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.toolFlagsResolver = resolver
	}
}

// WithSkillExecToolsDisabled tells the processor that skill_exec and its
// companion session tools were not registered (e.g. because the executor
// does not support interactive sessions).  The processor omits the
// corresponding guidance lines so the model is never taught to use tools
// it cannot call.
func WithSkillExecToolsDisabled() SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.execToolsDisabled = true
	}
}

// WithSkillsRepositoryResolver sets an invocation-aware repository resolver.
func WithSkillsRepositoryResolver(
	resolver func(*agent.Invocation) skill.Repository,
) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.repoResolver = resolver
	}
}

// WithMaxLoadedSkills caps how many skills remain "loaded" in session
// state.
//
// When max <= 0, no cap is applied (default behavior).
//
// When max > 0, the processor keeps at most max most-recently touched
// skills and offloads the rest by clearing their state keys.
func WithMaxLoadedSkills(max int) SkillsRequestProcessorOption {
	return func(o *skillsRequestProcessorOptions) {
		o.maxLoadedSkills = max
	}
}

// SkillsRequestProcessor injects skill overviews and loaded contents.
//
// Behavior:
//   - Overview: injects names + descriptions (cheap).
//   - Loaded skills: inject full SKILL.md body.
//   - Docs: inject doc texts selected via state keys.
//
// State keys used (per agent, ephemeral):
//   - skill.LoadedKey(agentName, skillName) -> "1"
//   - skill.DocsKey(agentName, skillName) ->
//     "*" or JSON array of file names.
type SkillsRequestProcessor struct {
	repo              skill.Repository
	repoResolver      func(*agent.Invocation) skill.Repository
	toolingGuidance   *string
	loadMode          string
	toolResultMode    bool
	maxLoadedSkills   int
	toolFlags         skillprofile.Flags
	toolFlagsResolver func(*agent.Invocation) skillprofile.Flags
}

const (
	skillsTurnInitStateKey = "processor:skills:turn_init"
)

// NewSkillsRequestProcessor creates a processor instance.
func NewSkillsRequestProcessor(
	repo skill.Repository,
	opts ...SkillsRequestProcessorOption,
) *SkillsRequestProcessor {
	var options skillsRequestProcessorOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&options)
	}

	flags := options.toolFlags
	if !options.hasToolFlags {
		var err error
		flags, err = skillprofile.ResolveFlags(options.toolProfile, nil)
		if err != nil {
			flags = skillprofile.Flags{}
		}
	}
	if options.execToolsDisabled {
		flags = flags.WithoutInteractiveExecution()
	}
	return &SkillsRequestProcessor{
		repo:              repo,
		repoResolver:      options.repoResolver,
		toolingGuidance:   options.toolingGuidance,
		loadMode:          normalizeSkillLoadMode(options.loadMode),
		toolResultMode:    options.toolResultMode,
		maxLoadedSkills:   options.maxLoadedSkills,
		toolFlags:         flags,
		toolFlagsResolver: options.toolFlagsResolver,
	}
}

func normalizeSkillLoadMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case SkillLoadModeOnce:
		return SkillLoadModeOnce
	case SkillLoadModeTurn:
		return SkillLoadModeTurn
	case SkillLoadModeSession:
		return SkillLoadModeSession
	default:
		return defaultSkillLoadMode
	}
}

// ProcessRequest implements flow.RequestProcessor.
func (p *SkillsRequestProcessor) ProcessRequest(
	ctx context.Context, inv *agent.Invocation, req *model.Request,
	ch chan<- *event.Event,
) {
	repo := p.repositoryForInvocation(inv)
	if req == nil || inv == nil || inv.Session == nil || repo == nil {
		return
	}

	maybeMigrateLegacySkillState(ctx, inv, ch)

	p.maybeClearSkillStateForTurn(ctx, inv, ch)

	// 1) Always inject overview (names + descriptions) into system
	//    message. Merge into existing system message if present.
	p.injectOverview(ctx, inv, req, repo)

	loaded := p.getLoadedSkills(inv)
	loaded = p.maybeCapLoadedSkills(ctx, inv, loaded, ch)

	if p.toolResultMode {
		// Loaded skill bodies/docs are materialized into tool results by a
		// post-content request processor.
		agent.EmitEvent(ctx, inv, ch, event.New(
			inv.InvocationID, inv.AgentName,
			event.WithObject(model.ObjectTypePreprocessingInstruction),
		))
		return
	}

	// 2) Loaded skills full content (merge into existing system message).
	sort.Strings(loaded) // stable prompt order

	var lb strings.Builder
	for _, name := range loaded {
		sk, err := skill.GetForContext(ctx, repo, name)
		if err != nil || sk == nil {
			log.WarnfContext(
				ctx,
				"skills: get %s failed: %v",
				name,
				err,
			)
			continue
		}
		if sk.Body != "" {
			lb.WriteString("\n[Loaded] ")
			lb.WriteString(name)
			lb.WriteString("\n\n")
			lb.WriteString(sk.Body)
			lb.WriteString("\n")
		}
		// Docs
		sel := p.getDocsSelection(ctx, inv, name)
		// Summary line to make selected docs explicit.
		lb.WriteString("Docs loaded: ")
		if len(sel) == 0 {
			lb.WriteString("none\n")
		} else {
			lb.WriteString(strings.Join(sel, ", "))
			lb.WriteString("\n")
		}
		if len(sel) > 0 {
			if docText := p.buildDocsText(sk, sel); docText != "" {
				lb.WriteString(docText)
			}
		}
	}
	if s := lb.String(); s != "" {
		p.mergeIntoSystem(req, s)
	}

	p.maybeOffloadLoadedSkills(ctx, inv, loaded, ch)

	// Send a preprocessing trace event even when only overview is
	// injected, for consistent trace semantics.
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID, inv.AgentName,
		event.WithObject(model.ObjectTypePreprocessingInstruction),
	))
}

func (p *SkillsRequestProcessor) repositoryForInvocation(
	inv *agent.Invocation,
) skill.Repository {
	if p.repoResolver != nil {
		return p.repoResolver(inv)
	}
	return p.repo
}

func (p *SkillsRequestProcessor) maybeCapLoadedSkills(
	ctx context.Context,
	inv *agent.Invocation,
	loaded []string,
	ch chan<- *event.Event,
) []string {
	if p.maxLoadedSkills <= 0 || len(loaded) <= p.maxLoadedSkills {
		return loaded
	}
	if inv == nil || inv.Session == nil {
		return loaded
	}

	keep := keepMostRecentSkills(
		inv,
		loaded,
		p.maxLoadedSkills,
	)
	if len(keep) == 0 {
		return loaded
	}
	keepSet := make(map[string]struct{}, len(keep))
	for _, name := range keep {
		keepSet[name] = struct{}{}
	}

	delta := make(map[string][]byte, len(loaded)*2+1)
	var kept []string
	for _, name := range loaded {
		if _, ok := keepSet[name]; ok {
			kept = append(kept, name)
			continue
		}
		loadedKey := skill.LoadedKey(inv.AgentName, name)
		inv.Session.SetState(loadedKey, nil)
		delta[loadedKey] = nil

		docsKey := skill.DocsKey(inv.AgentName, name)
		inv.Session.SetState(docsKey, nil)
		delta[docsKey] = nil
	}
	orderKey := skill.LoadedOrderKey(inv.AgentName)
	orderValue := skill.MarshalLoadedOrder(keep)
	inv.Session.SetState(orderKey, orderValue)
	delta[orderKey] = orderValue
	if len(delta) > 0 {
		agent.EmitEvent(ctx, inv, ch, event.New(
			inv.InvocationID,
			inv.AgentName,
			event.WithObject(model.ObjectTypeStateUpdate),
			event.WithStateDelta(delta),
		))
	}
	return kept
}

func keepMostRecentSkills(
	inv *agent.Invocation,
	loaded []string,
	max int,
) []string {
	if inv == nil || inv.Session == nil || max <= 0 {
		return nil
	}

	order := loadedSkillOrder(inv, loaded)
	if len(order) == 0 {
		return nil
	}
	if len(order) <= max {
		return append([]string(nil), order...)
	}
	return append([]string(nil), order[len(order)-max:]...)
}

func loadedSkillSet(loaded []string) map[string]struct{} {
	out := make(map[string]struct{}, len(loaded))
	for _, name := range loaded {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func loadedSkillOrder(
	inv *agent.Invocation,
	loaded []string,
) []string {
	if inv == nil || inv.Session == nil {
		return nil
	}

	loadedSet := loadedSkillSet(loaded)
	if len(loadedSet) == 0 {
		return nil
	}

	order := loadedSkillOrderFromState(inv, loadedSet)
	if len(order) < len(loadedSet) {
		order = appendSkillsToOrderFromEvents(
			order,
			inv.Session.GetEvents(),
			inv.AgentName,
			loadedSet,
		)
	}
	if len(order) < len(loadedSet) {
		order = fillLoadedSkillOrderAlphabetically(
			order,
			loadedSet,
		)
	}
	return order
}

func loadedSkillOrderFromState(
	inv *agent.Invocation,
	loadedSet map[string]struct{},
) []string {
	if inv == nil || inv.Session == nil {
		return nil
	}

	raw, ok := inv.Session.GetState(skill.LoadedOrderKey(inv.AgentName))
	if !ok || len(raw) == 0 {
		return nil
	}

	order := skill.ParseLoadedOrder(raw)
	if len(order) == 0 {
		return nil
	}

	out := make([]string, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, name := range order {
		if _, ok := loadedSet[name]; !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, name)
		seen[name] = struct{}{}
	}
	return out
}

func appendSkillsToOrderFromEvents(
	order []string,
	events []event.Event,
	agentName string,
	loadedSet map[string]struct{},
) []string {
	for i := 0; i < len(events); i++ {
		order = appendSkillsToOrderFromToolResponseEvent(
			events[i],
			agentName,
			loadedSet,
			order,
		)
	}
	return order
}

func appendSkillsToOrderFromToolResponseEvent(
	ev event.Event,
	agentName string,
	loadedSet map[string]struct{},
	order []string,
) []string {
	if agentName != "" && ev.Author != agentName {
		return order
	}
	if ev.Response == nil {
		return order
	}
	if ev.Object != model.ObjectTypeToolResponse {
		return order
	}
	if len(ev.Choices) == 0 {
		return order
	}

	for j := 0; j < len(ev.Choices); j++ {
		msg := ev.Choices[j].Message
		if msg.Role != model.RoleTool {
			continue
		}
		if msg.ToolName != skillToolLoad &&
			msg.ToolName != skillToolSelectDocs {
			continue
		}
		name := skillNameFromToolResponse(msg)
		if name == "" {
			continue
		}
		if _, ok := loadedSet[name]; !ok {
			continue
		}
		order = skill.TouchLoadedOrder(order, name)
	}
	return order
}

func fillLoadedSkillOrderAlphabetically(
	order []string,
	loadedSet map[string]struct{},
) []string {
	seen := loadedSkillSet(order)
	var sorted []string
	for name := range loadedSet {
		if _, ok := seen[name]; ok {
			continue
		}
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return append(order, sorted...)
}

func skillNameFromToolResponse(msg model.Message) string {
	switch msg.ToolName {
	case skillToolLoad:
		return parseLoadedSkillFromText(msg.Content)
	case skillToolSelectDocs:
		var in skillNameInput
		if err := json.Unmarshal([]byte(msg.Content), &in); err != nil {
			return ""
		}
		return strings.TrimSpace(in.Skill)
	default:
		return ""
	}
}

func (p *SkillsRequestProcessor) maybeClearSkillStateForTurn(
	ctx context.Context,
	inv *agent.Invocation,
	ch chan<- *event.Event,
) {
	if p.loadMode != SkillLoadModeTurn || inv == nil || inv.Session == nil {
		return
	}
	if _, ok := inv.GetState(skillsTurnInitStateKey); ok {
		return
	}
	inv.SetState(skillsTurnInitStateKey, true)

	delta := clearSkillState(inv)
	if len(delta) == 0 {
		return
	}
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(delta),
	))
}

func clearSkillState(inv *agent.Invocation) map[string][]byte {
	if inv == nil || inv.Session == nil {
		return nil
	}
	state := inv.Session.SnapshotState()
	if len(state) == 0 {
		return nil
	}
	delta := make(map[string][]byte)
	loadedPrefix := skill.LoadedPrefix(inv.AgentName)
	docsPrefix := skill.DocsPrefix(inv.AgentName)
	orderKey := skill.LoadedOrderKey(inv.AgentName)
	for k, v := range state {
		if !strings.HasPrefix(k, loadedPrefix) &&
			!strings.HasPrefix(k, docsPrefix) &&
			k != orderKey {
			continue
		}
		if len(v) == 0 {
			continue
		}
		inv.Session.SetState(k, nil)
		delta[k] = nil
	}
	return delta
}

func (p *SkillsRequestProcessor) maybeOffloadLoadedSkills(
	ctx context.Context,
	inv *agent.Invocation,
	loaded []string,
	ch chan<- *event.Event,
) {
	if p.loadMode != SkillLoadModeOnce ||
		inv == nil ||
		inv.Session == nil ||
		len(loaded) == 0 {
		return
	}
	delta := make(map[string][]byte, len(loaded)*2+1)
	for _, name := range loaded {
		loadedKey := skill.LoadedKey(inv.AgentName, name)
		inv.Session.SetState(loadedKey, nil)
		delta[loadedKey] = nil

		docsKey := skill.DocsKey(inv.AgentName, name)
		inv.Session.SetState(docsKey, nil)
		delta[docsKey] = nil
	}
	orderKey := skill.LoadedOrderKey(inv.AgentName)
	inv.Session.SetState(orderKey, nil)
	delta[orderKey] = nil
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(delta),
	))
}

func (p *SkillsRequestProcessor) injectOverview(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	repo skill.Repository,
) {
	sums := skill.SummariesForContext(ctx, repo)
	if len(sums) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(skillsOverviewHeader)
	b.WriteString("\n")
	for _, s := range sums {
		line := fmt.Sprintf("- %s: %s\n", s.Name, s.Description)
		b.WriteString(line)
	}
	flags := p.toolFlagsForInvocation(inv)
	if capability := p.capabilityGuidanceText(flags); capability != "" {
		b.WriteString(capability)
	}
	if guidance := p.toolingGuidanceText(flags); guidance != "" {
		b.WriteString(guidance)
	}
	overview := b.String()

	idx := findSystemMessageIndex(req.Messages)
	if idx >= 0 {
		sys := &req.Messages[idx]
		if !strings.Contains(sys.Content, skillsOverviewHeader) {
			if sys.Content != "" {
				sys.Content += "\n\n" + overview
			} else {
				sys.Content = overview
			}
		}
		return
	}
	// No system message yet: create one at the front.
	msg := model.NewSystemMessage(overview)
	req.Messages = append([]model.Message{msg}, req.Messages...)
}

func (p *SkillsRequestProcessor) toolFlagsForInvocation(
	inv *agent.Invocation,
) skillprofile.Flags {
	if p.toolFlagsResolver != nil {
		return p.toolFlagsResolver(inv)
	}
	return p.toolFlags
}

func (p *SkillsRequestProcessor) toolingGuidanceText(
	flags skillprofile.Flags,
) string {
	if p.toolingGuidance == nil {
		return defaultToolingAndWorkspaceGuidance(flags)
	}
	return normalizeGuidance(*p.toolingGuidance)
}

func (p *SkillsRequestProcessor) capabilityGuidanceText(
	flags skillprofile.Flags,
) string {
	if p.toolingGuidance != nil && *p.toolingGuidance == "" {
		return ""
	}
	if flags.Run {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsCapabilityHeader)
	b.WriteString("\n")
	if flags.Load {
		b.WriteString("- This configuration supports skill discovery and ")
		b.WriteString("knowledge loading only.\n")
		b.WriteString("- Built-in skill execution tools are unavailable in ")
		b.WriteString("the current mode.\n")
		b.WriteString("- If a loaded skill describes scripts, shell commands, ")
		b.WriteString("workspace paths, generated files, or interactive ")
		b.WriteString("flows, treat that content as reference only. Use ")
		b.WriteString("other registered tools for real actions, or explain ")
		b.WriteString("that execution is unavailable in the current mode.\n")
		return b.String()
	}
	if flags.HasDocHelpers() {
		b.WriteString("- This configuration supports skill discovery and ")
		b.WriteString("skill doc inspection only.\n")
		b.WriteString("- Built-in skill loading and execution tools are ")
		b.WriteString("unavailable in the current mode.\n")
		b.WriteString("- Listing or selecting docs does not inject SKILL.md ")
		b.WriteString("or doc contents into model context by itself.\n")
		return b.String()
	}
	b.WriteString("- This configuration exposes skill summaries only. ")
	b.WriteString("Built-in skill tools are unavailable in the current ")
	b.WriteString("mode.\n")
	b.WriteString("- Treat the skill overview as a catalog of possible ")
	b.WriteString("capabilities. Use other registered tools, or explain ")
	b.WriteString("the limitation clearly when the task depends on ")
	b.WriteString("skill loading or execution.\n")
	return b.String()
}

func defaultToolingAndWorkspaceGuidance(flags skillprofile.Flags) string {
	if !flags.Any() {
		return defaultCatalogOnlyGuidance()
	}
	if !flags.Run {
		if flags.Load {
			return defaultKnowledgeOnlyGuidance(flags)
		}
		if flags.HasDocHelpers() {
			return defaultDocHelpersOnlyGuidance(flags)
		}
		return defaultCatalogOnlyGuidance()
	}
	return defaultFullToolingAndWorkspaceGuidance(flags)
}

func defaultCatalogOnlyGuidance() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsToolingGuidanceHeader)
	b.WriteString("\n")
	b.WriteString("- Use the skill overview as a catalog only. Built-in ")
	b.WriteString("skill tools are unavailable in this configuration; ")
	b.WriteString("if a task depends on loading or executing a skill, ")
	b.WriteString("use other registered tools or explain the limitation ")
	b.WriteString("clearly.\n")
	return b.String()
}

func defaultKnowledgeOnlyGuidance(flags skillprofile.Flags) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsToolingGuidanceHeader)
	b.WriteString("\n")
	b.WriteString("- Use skills for progressive disclosure only: load ")
	b.WriteString("SKILL.md first, then inspect only the documentation ")
	b.WriteString("needed for the current task.\n")
	appendKnowledgeGuidance(&b, flags)
	b.WriteString("- Treat loaded skill content as domain guidance. Do ")
	b.WriteString("not claim you executed scripts, shell commands, or ")
	b.WriteString("interactive flows described by the skill.\n")
	b.WriteString("- If a skill depends on execution to complete the ")
	b.WriteString("task, switch to other registered tools (for example, ")
	b.WriteString("MCP tools) or explain the limitation clearly.\n")
	return b.String()
}

func defaultDocHelpersOnlyGuidance(flags skillprofile.Flags) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsToolingGuidanceHeader)
	b.WriteString("\n")
	switch {
	case flags.ListDocs && flags.SelectDocs:
		b.WriteString("- Use skills only to inspect available doc names or ")
		b.WriteString("adjust doc selection state.\n")
	case flags.ListDocs:
		b.WriteString("- Use skills only to inspect available doc names.\n")
	case flags.SelectDocs:
		b.WriteString("- Use skills only to adjust doc selection when doc ")
		b.WriteString("names are already known.\n")
	}
	b.WriteString("- Built-in skill loading is unavailable, so doc ")
	b.WriteString("helpers do not inject SKILL.md or doc contents into ")
	b.WriteString("context; if the task needs loaded content or ")
	b.WriteString("execution, use other registered tools or explain ")
	b.WriteString("the limitation clearly.\n")
	return b.String()
}

func defaultFullToolingAndWorkspaceGuidance(flags skillprofile.Flags) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsToolingGuidanceHeader)
	b.WriteString("\n")
	b.WriteString("- Skills run inside an isolated workspace; you see ")
	b.WriteString("only files that are in the workspace or have been ")
	b.WriteString("staged there by tools.\n")
	b.WriteString("- skill_run runs with CWD at the skill root by ")
	b.WriteString("default; avoid setting cwd unless needed.\n")
	b.WriteString("- If you set cwd, use $SKILLS_DIR/$SKILL_NAME (or ")
	b.WriteString("a subdir). $SKILLS_DIR alone is the parent dir.\n")
	b.WriteString("- Prefer $WORK_DIR, $OUTPUT_DIR, $RUN_DIR, and ")
	b.WriteString("$WORKSPACE_DIR over hard-coded paths.\n")
	b.WriteString("- Treat $WORK_DIR/inputs (and a skill's inputs/ ")
	b.WriteString("directory) as the place where tools stage user or ")
	b.WriteString("host input files. Avoid overwriting or mutating ")
	b.WriteString("these inputs directly.\n")
	b.WriteString("- User-uploaded file inputs in the conversation ")
	b.WriteString("are automatically staged into $WORK_DIR/inputs ")
	b.WriteString("when skill_run executes.\n")
	b.WriteString("- When the user mentions external files, ")
	b.WriteString("directories, artifacts, or URLs, decide whether to ")
	b.WriteString("stage them into $WORK_DIR/inputs via available ")
	b.WriteString("tools before reading.\n")
	b.WriteString("- To map external files into the workspace, use ")
	b.WriteString("skill_run inputs (artifact://, host://, ")
	b.WriteString("workspace://, skill://). For artifacts, prefer ")
	b.WriteString("artifact://name@version; inputs[*].pin=true ")
	b.WriteString("reuses the first resolved version (best effort).\n")
	b.WriteString("- Prefer writing new files under $OUTPUT_DIR or a ")
	b.WriteString("skill's out/ directory and include output_files ")
	b.WriteString("globs (or an outputs spec) so files can be ")
	b.WriteString("collected or saved as artifacts.\n")
	b.WriteString("- Use stdout/stderr for logs or short status text. ")
	b.WriteString("If the model needs large or structured text, ")
	b.WriteString("write it to files under $OUTPUT_DIR and return it ")
	b.WriteString("via output_files or outputs.\n")
	b.WriteString("- For Python skills that need third-party ")
	b.WriteString("packages, create a virtualenv under the ")
	b.WriteString("skill's .venv/ directory (it is writable inside ")
	b.WriteString("the workspace).\n")
	b.WriteString("- output_files entries are workspace paths/globs ")
	b.WriteString("(e.g. out/*.txt). Do not use workspace:// or ")
	b.WriteString("artifact:// in output_files.\n")
	b.WriteString("- When skill_run returns primary_output or ")
	b.WriteString("output_files, prefer using the inline content ")
	b.WriteString("directly. ")
	b.WriteString("If you need a stable reference for other tools, ")
	b.WriteString("use output_files[*].ref (workspace://...).\n")
	b.WriteString("- Non-text outputs never inline content. Use ")
	b.WriteString("output_files[*].ref (workspace://...) to pass ")
	b.WriteString("them to other tools. For large text outputs, set ")
	b.WriteString("omit_inline_content=true so output_files return ")
	b.WriteString("metadata only, then use output_files[*].ref with ")
	b.WriteString("read_file when needed. For persistence, prefer ")
	b.WriteString("outputs.save=true with outputs.inline=false; if ")
	b.WriteString("you use output_files, set save_as_artifacts=true.\n")
	b.WriteString("- Do not rerun the same skill_run command when you ")
	b.WriteString("already have the needed content.\n")
	b.WriteString("- If you already have the needed file content, ")
	b.WriteString("stop calling file tools and answer.\n")
	b.WriteString("- When chaining multiple skills, read previous ")
	b.WriteString("results from $OUTPUT_DIR (or a skill's out/ ")
	b.WriteString("directory) instead of copying them back into ")
	b.WriteString("inputs directories.\n")
	if flags.Load {
		b.WriteString("- Treat loaded skill docs as guidance, not perfect ")
		b.WriteString("truth; when runtime help or stderr disagrees, trust ")
		b.WriteString("observed runtime behavior.\n")
		b.WriteString("- Loading a skill gives you instructions and bundled ")
		b.WriteString("resources; it does not execute the skill by itself.\n")
		b.WriteString("- The skill summaries above are routing summaries ")
		b.WriteString("only; they do not replace SKILL.md or other loaded ")
		b.WriteString("docs.\n")
		b.WriteString("- If the loaded content already provides enough ")
		b.WriteString("guidance to answer or produce the requested result, ")
		b.WriteString("respond directly.\n")
		b.WriteString("- If you decide to use a skill, load SKILL.md before ")
		if flags.RequiresExecSessionTools() {
			b.WriteString("the first skill_run or skill_exec for that skill, ")
		} else {
			b.WriteString("the first skill_run for that skill, ")
		}
		b.WriteString("then load only the docs you still need.\n")
		b.WriteString("- Do not infer commands, script entrypoints, or ")
		b.WriteString("resource layouts from the short summary alone.\n")
		appendKnowledgeGuidance(&b, flags)
	} else if flags.HasDocHelpers() {
		b.WriteString("- Built-in skill loading is unavailable in this ")
		b.WriteString("configuration. Doc listing or selection helpers ")
		b.WriteString("can inspect doc names or selection state, but they ")
		b.WriteString("do not inject SKILL.md or doc contents into ")
		b.WriteString("context.\n")
	} else {
		b.WriteString("- Built-in skill loading is unavailable in this ")
		b.WriteString("configuration; do not assume SKILL.md or doc ")
		b.WriteString("contents are in context.\n")
	}
	b.WriteString("- Use execution tools only when running a command will ")
	b.WriteString("reveal or produce information or files you still ")
	b.WriteString("need.\n")
	if flags.RequiresExecSessionTools() {
		b.WriteString("- Use skill_exec only when a command needs ")
		b.WriteString("incremental stdin or TTY-style interaction; ")
		b.WriteString("otherwise prefer one-shot execution.\n")
	} else {
		b.WriteString("- Do not assume interactive execution is available ")
		b.WriteString("when only one-shot execution tools are present.\n")
	}
	b.WriteString("- skill_run is a command runner inside the skill ")
	b.WriteString("workspace, not a magic capability. It does not ")
	b.WriteString("automatically add the skill directory to PATH or ")
	b.WriteString("install dependencies; invoke scripts via an explicit ")
	b.WriteString("interpreter and path (e.g., python3 scripts/foo.py).\n")
	b.WriteString("- When you execute, follow the tool description, ")
	if flags.Load {
		b.WriteString("loaded skill docs, ")
	}
	b.WriteString("bundled scripts, and observed runtime behavior ")
	b.WriteString("rather than inventing shell syntax or command ")
	b.WriteString("arguments.\n")
	return b.String()
}

func appendKnowledgeGuidance(
	b *strings.Builder,
	flags skillprofile.Flags,
) {
	if !flags.Load {
		return
	}
	switch {
	case flags.ListDocs && flags.SelectDocs:
		b.WriteString("- Use the available doc listing and selection helpers ")
		b.WriteString("to keep documentation loads targeted.\n")
	case flags.ListDocs:
		b.WriteString("- Use the available doc listing helper to discover ")
		b.WriteString("doc names, then load only the docs you need.\n")
	case flags.SelectDocs:
		b.WriteString("- If doc names are already known, use the available ")
		b.WriteString("doc selection helper to keep loaded docs targeted.\n")
	default:
		b.WriteString("- If you need docs, request them directly with ")
		b.WriteString("skill_load.docs or include_all_docs.\n")
	}
	b.WriteString("- Avoid include_all_docs unless the user asks or the ")
	b.WriteString("task genuinely needs the full doc set.\n")
}

func normalizeGuidance(guidance string) string {
	if guidance == "" {
		return ""
	}
	if !strings.HasPrefix(guidance, "\n") {
		guidance = "\n" + guidance
	}
	if !strings.HasSuffix(guidance, "\n") {
		guidance += "\n"
	}
	return guidance
}

func (p *SkillsRequestProcessor) getLoadedSkills(
	inv *agent.Invocation,
) []string {
	var names []string
	prefix := skill.LoadedPrefix(inv.AgentName)
	state := inv.Session.SnapshotState()
	for k, v := range state {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if len(v) == 0 {
			continue
		}
		name := strings.TrimPrefix(k, prefix)
		names = append(names, name)
	}
	return names
}

func (p *SkillsRequestProcessor) getDocsSelection(
	ctx context.Context,
	inv *agent.Invocation,
	name string,
) []string {
	repo := p.repositoryForInvocation(inv)
	if repo == nil {
		return nil
	}
	key := skill.DocsKey(inv.AgentName, name)
	v, ok := inv.Session.GetState(key)
	if !ok || len(v) == 0 {
		return nil
	}
	if string(v) == "*" {
		// Select all doc files present.
		sk, err := skill.GetForContext(ctx, repo, name)
		if err != nil || sk == nil {
			return nil
		}
		var all []string
		for _, d := range sk.Docs {
			all = append(all, d.Path)
		}
		return all
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err != nil {
		return nil
	}
	return arr
}

func (p *SkillsRequestProcessor) buildDocsText(
	sk *skill.Skill, wanted []string,
) string {
	if sk == nil || len(sk.Docs) == 0 {
		return ""
	}
	// Build a map for quick lookup of requested docs.
	want := map[string]struct{}{}
	for _, n := range wanted {
		want[n] = struct{}{}
	}
	var b strings.Builder
	for _, d := range sk.Docs {
		if _, ok := want[d.Path]; !ok {
			continue
		}
		if d.Content == "" {
			continue
		}
		// Separate docs with a marker title.
		b.WriteString("\n[Doc] ")
		b.WriteString(d.Path)
		b.WriteString("\n\n")
		b.WriteString(d.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// mergeIntoSystem appends content into the existing system message when
// available; otherwise, it creates a new system message at the front.
func (p *SkillsRequestProcessor) mergeIntoSystem(
	req *model.Request, content string,
) {
	if req == nil || content == "" {
		return
	}
	idx := findSystemMessageIndex(req.Messages)
	if idx >= 0 {
		if req.Messages[idx].Content != "" {
			req.Messages[idx].Content += "\n\n" + content
		} else {
			req.Messages[idx].Content = content
		}
		return
	}
	msg := model.NewSystemMessage(content)
	req.Messages = append([]model.Message{msg}, req.Messages...)
}
