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
	execToolsDisabled bool
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
//   - Configured with empty string: omit the guidance block.
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

// WithMaxLoadedSkills caps how many skills remain "loaded" in session
// state.
//
// When max <= 0, no cap is applied (default behavior).
//
// When max > 0, the processor keeps at most max most-recently loaded
// skills (skill_load / skill_select_docs) and offloads the rest by
// clearing their state keys.
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
	toolingGuidance   *string
	loadMode          string
	toolResultMode    bool
	maxLoadedSkills   int
	toolProfile       string
	execToolsDisabled bool
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
	return &SkillsRequestProcessor{
		repo:              repo,
		toolingGuidance:   options.toolingGuidance,
		loadMode:          normalizeSkillLoadMode(options.loadMode),
		toolResultMode:    options.toolResultMode,
		maxLoadedSkills:   options.maxLoadedSkills,
		toolProfile:       skillprofile.Normalize(options.toolProfile),
		execToolsDisabled: options.execToolsDisabled,
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
	if req == nil || inv == nil || inv.Session == nil || p.repo == nil {
		return
	}

	maybeMigrateLegacySkillState(ctx, inv, ch)

	p.maybeClearSkillStateForTurn(ctx, inv, ch)

	// 1) Always inject overview (names + descriptions) into system
	//    message. Merge into existing system message if present.
	p.injectOverview(req)

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
		sk, err := p.repo.Get(name)
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
		sel := p.getDocsSelection(inv, name)
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

	delta := make(map[string][]byte, len(loaded)*2)
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

	loadedSet := loadedSkillSet(loaded)
	if len(loadedSet) == 0 {
		return nil
	}

	keep, seen := mostRecentSkillsFromEvents(
		inv.Session.GetEvents(),
		inv.AgentName,
		loadedSet,
		max,
	)
	if len(keep) >= max {
		return keep
	}
	return fillSkillsAlphabetically(keep, seen, loaded, max)
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

func mostRecentSkillsFromEvents(
	events []event.Event,
	agentName string,
	loadedSet map[string]struct{},
	max int,
) ([]string, map[string]struct{}) {
	keep := make([]string, 0, max)
	seen := make(map[string]struct{}, max)
	for i := len(events) - 1; i >= 0 && len(keep) < max; i-- {
		keep = appendSkillsFromToolResponseEvent(
			events[i],
			agentName,
			loadedSet,
			seen,
			keep,
			max,
		)
	}
	return keep, seen
}

func appendSkillsFromToolResponseEvent(
	ev event.Event,
	agentName string,
	loadedSet map[string]struct{},
	seen map[string]struct{},
	keep []string,
	max int,
) []string {
	if agentName != "" && ev.Author != agentName {
		return keep
	}
	if ev.Response == nil {
		return keep
	}
	if ev.Object != model.ObjectTypeToolResponse {
		return keep
	}
	if len(ev.Choices) == 0 {
		return keep
	}

	for j := len(ev.Choices) - 1; j >= 0 && len(keep) < max; j-- {
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
		if _, ok := seen[name]; ok {
			continue
		}
		keep = append(keep, name)
		seen[name] = struct{}{}
	}
	return keep
}

func fillSkillsAlphabetically(
	keep []string,
	seen map[string]struct{},
	loaded []string,
	max int,
) []string {
	sorted := append([]string(nil), loaded...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if len(keep) == max {
			break
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		keep = append(keep, name)
		seen[name] = struct{}{}
	}
	return keep
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
	for k, v := range state {
		if !strings.HasPrefix(k, loadedPrefix) &&
			!strings.HasPrefix(k, docsPrefix) {
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
	delta := make(map[string][]byte, len(loaded)*2)
	for _, name := range loaded {
		loadedKey := skill.LoadedKey(inv.AgentName, name)
		inv.Session.SetState(loadedKey, nil)
		delta[loadedKey] = nil

		docsKey := skill.DocsKey(inv.AgentName, name)
		inv.Session.SetState(docsKey, nil)
		delta[docsKey] = nil
	}
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(delta),
	))
}

func (p *SkillsRequestProcessor) injectOverview(req *model.Request) {
	sums := p.repo.Summaries()
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
	if capability := p.capabilityGuidanceText(); capability != "" {
		b.WriteString(capability)
	}
	if guidance := p.toolingGuidanceText(); guidance != "" {
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

func (p *SkillsRequestProcessor) toolingGuidanceText() string {
	if p.toolingGuidance == nil {
		return defaultToolingAndWorkspaceGuidance(
			p.toolProfile, p.execToolsDisabled,
		)
	}
	return normalizeGuidance(*p.toolingGuidance)
}

func (p *SkillsRequestProcessor) capabilityGuidanceText() string {
	if !skillprofile.IsKnowledgeOnly(p.toolProfile) {
		return ""
	}
	if p.toolingGuidance != nil && *p.toolingGuidance == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsCapabilityHeader)
	b.WriteString("\n")
	b.WriteString("- This profile supports skill discovery and knowledge ")
	b.WriteString("loading only.\n")
	b.WriteString("- Execution-oriented skill tools are unavailable in ")
	b.WriteString("the current mode.\n")
	b.WriteString("- If a loaded skill describes scripts, shell commands, ")
	b.WriteString("workspace paths, generated files, or interactive flows, ")
	b.WriteString("treat that content as reference only. Use other ")
	b.WriteString("registered tools for real actions, or explain that ")
	b.WriteString("execution is unavailable in the current mode.\n")
	return b.String()
}

func defaultToolingAndWorkspaceGuidance(
	profile string, execToolsDisabled bool,
) string {
	if skillprofile.IsKnowledgeOnly(profile) {
		return defaultKnowledgeOnlyGuidance()
	}
	return defaultFullToolingAndWorkspaceGuidance(execToolsDisabled)
}

func defaultKnowledgeOnlyGuidance() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(skillsToolingGuidanceHeader)
	b.WriteString("\n")
	b.WriteString("- Use skills for progressive disclosure only: load ")
	b.WriteString("SKILL.md first, then inspect only the documentation ")
	b.WriteString("needed for the current task.\n")
	b.WriteString("- Avoid include_all_docs unless the user asks or the ")
	b.WriteString("task genuinely needs the full doc set.\n")
	b.WriteString("- Treat loaded skill content as domain guidance. Do ")
	b.WriteString("not claim you executed scripts, shell commands, or ")
	b.WriteString("interactive flows described by the skill.\n")
	b.WriteString("- If a skill depends on execution to complete the ")
	b.WriteString("task, switch to other registered tools (for example, ")
	b.WriteString("MCP tools) or explain the limitation clearly.\n")
	return b.String()
}

func defaultFullToolingAndWorkspaceGuidance(execToolsDisabled bool) string {
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
	b.WriteString("- Treat loaded skill docs as guidance, not perfect ")
	b.WriteString("truth; when runtime help or stderr disagrees, trust ")
	b.WriteString("observed runtime behavior.\n")
	b.WriteString("- Prefer commands or scripts bundled inside the ")
	b.WriteString("skill workspace when they exist; they are more ")
	b.WriteString("stable than ad hoc shell built around external ")
	b.WriteString("CLIs.\n")
	b.WriteString("- Progressive disclosure: call skill_load with only ")
	b.WriteString("skill first.\n")
	b.WriteString("- For docs, prefer skill_list_docs + ")
	b.WriteString("skill_select_docs to load only what you need.\n")
	b.WriteString("- Avoid include_all_docs unless you need every doc ")
	b.WriteString("or the user asks.\n")
	b.WriteString("- Use skill_run primarily for commands required by ")
	b.WriteString("the skill docs or bundled scripts, plus the minimal ")
	b.WriteString("read-only probe commands needed to verify external ")
	b.WriteString("CLI behavior.\n")
	if !execToolsDisabled {
		b.WriteString("- Use skill_exec when a command may stay running, ")
		b.WriteString("prompt for input, or require incremental stdin/TTY ")
		b.WriteString("interaction. Then use skill_write_stdin or ")
		b.WriteString("skill_poll_session until it exits, and ")
		b.WriteString("skill_kill_session to stop it if needed.\n")
		b.WriteString("- For CLIs that launch $EDITOR, prefer editor_text ")
		b.WriteString("on skill_run or skill_exec instead of trying to ")
		b.WriteString("drive a full-screen editor through stdin.\n")
	} else {
		b.WriteString("- For CLIs that launch $EDITOR, prefer editor_text ")
		b.WriteString("on skill_run instead of trying to drive a ")
		b.WriteString("full-screen editor through stdin.\n")
	}
	b.WriteString("- Safe probe commands include patterns such as ")
	b.WriteString("`--help`, `-h`, `--version`, or `<subcommand> ")
	b.WriteString("--help` when exact syntax is uncertain or a command ")
	b.WriteString("fails.\n")
	b.WriteString("- Keep probes targeted and bounded; avoid broad ")
	b.WriteString("shell exploration when a small help query can ")
	b.WriteString("verify the contract.\n")
	b.WriteString("- Do not invent subcommands, flags, or positional ")
	b.WriteString("arguments that do not appear in the loaded skill ")
	b.WriteString("docs, bundled scripts, observed help text, or a ")
	b.WriteString("prior successful command.\n")
	b.WriteString("- skill_run is a command runner inside the skill ")
	b.WriteString("workspace, not a magic capability. It does not ")
	b.WriteString("automatically add the skill directory to PATH or ")
	b.WriteString("install dependencies; invoke scripts via an explicit ")
	b.WriteString("interpreter and path (e.g., python3 scripts/foo.py).\n")
	b.WriteString("- Read the skill_run tool description each time: if ")
	b.WriteString("it mentions allowed_commands/denied_commands (or ")
	b.WriteString("previews Allowed commands), then shell syntax is ")
	b.WriteString("disabled and the command must be a single executable ")
	b.WriteString("+ args only (no pipes/redirects/chaining, no bash ")
	b.WriteString("-c). Use env/cwd fields and split multi-step ")
	b.WriteString("workflows into multiple skill_run calls.\n")
	b.WriteString("- Before executing, avoid guessing command names, ")
	b.WriteString("script paths, or dependencies. If the exact ")
	b.WriteString("executable/path is not explicitly given by the ")
	b.WriteString("loaded SKILL.md/docs, first do a small, targeted ")
	b.WriteString("check to confirm it exists (e.g., list the relevant ")
	b.WriteString("directory, check file existence, or verify the ")
	b.WriteString("executable is on PATH). Under command restrictions, ")
	b.WriteString("use only allowed commands for these checks.\n")
	b.WriteString("- When skill_run fails, do not stop early. If the ")
	b.WriteString("tool returns an error (no structured result), read ")
	b.WriteString("it and adjust (often restriction violation or ")
	b.WriteString("missing skill_load). If it returns a structured ")
	b.WriteString("result, treat non-zero exit_code or timed_out as ")
	b.WriteString("failure; inspect stderr/warnings, verify assumptions ")
	b.WriteString("(files, PATH, deps), consult SKILL.md/docs, and ")
	b.WriteString("retry with an adjusted command. Avoid repeating the ")
	b.WriteString("exact same failing command; after a couple attempts, ")
	b.WriteString("explain what you checked and ask for missing ")
	b.WriteString("information.\n")
	b.WriteString("- When the body and needed docs are present, call ")
	b.WriteString("skill_run to execute or validate those commands.\n")
	b.WriteString("- If a CLI appears interactive-only and you cannot ")
	b.WriteString("confirm a non-interactive path, do not claim ")
	b.WriteString("success; explain the limitation and give the best ")
	b.WriteString("fallback.\n")
	return b.String()
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
	inv *agent.Invocation, name string,
) []string {
	key := skill.DocsKey(inv.AgentName, name)
	v, ok := inv.Session.GetState(key)
	if !ok || len(v) == 0 {
		return nil
	}
	if string(v) == "*" {
		// Select all doc files present.
		sk, err := p.repo.Get(name)
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
