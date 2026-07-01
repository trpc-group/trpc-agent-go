//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Reviewer examines a session delta and returns a ReviewDecision.
type Reviewer interface {
	Review(ctx context.Context, input *ReviewInput) (*ReviewDecision, error)
}

var reviewSystemPrompt = strings.Join([]string{
	"You are an evolution reviewer.",
	"Review the session transcript and decide whether reusable skills",
	"should be created, updated, or deleted in the managed skill library.",
	"",
	"Scope:",
	"- Evolution owns ONLY the skill library. Do NOT emit user facts, preferences, or episodic events here; durable memory is handled by a separate auto-memory pipeline against the same session.",
	"",
	"Guidelines:",
	"- Only save a skill when the workflow is reusable and non-trivial.",
	"- Do not save task-only progress, secrets, or one-off outputs.",
	"- The user message lists every existing skill with name, description, and a truncated body excerpt. Read the excerpts before deciding to create a new skill.",
	"",
	"Anti-proliferation rules (apply BEFORE deciding between `skills` and `updates`):",
	"- Compare your candidate's `steps` and `when_to_use` against each existing skill's body excerpt, not just the names. Quantitative differences (item counts, scale levels, sizes, region/entity lists, parameter values) are NOT a reason to create a new skill — they belong inside the body of the existing skill, e.g. as a parameter or as an explicit \"works for N items\" note.",
	"- If your proposed name is a strict superset of an existing name (e.g. existing `Foo Workflow`, proposed `Foo Workflow - X` / `Foo Workflow (3 items)` / `Foo Workflow v2`), this is a strong signal you should emit an `updates` entry against the existing name instead of a new `skills` entry. Suffixes that encode a specific task instance, scale, or run id are not allowed in skill names.",
	"- A new `skills` entry is only justified when the workflow itself is genuinely different (different tools, different output shape, different ordering constraint), not when only the input scale or named entities change.",
	"- If the existing library already covers your candidate and there is nothing new to add (no new pitfall, no broader applicability, no corrected step), prefer `skip_reason` over emitting a near-duplicate `updates` entry just to record the run.",
	"",
	"MANDATORY naming convention (enforced by the approval gate — violations will be rejected):",
	"- Skill names MUST be generic and parameterized. NEVER put a specific count, city list, dish list, country list, or entity list in the name.",
	"- WRONG: `Weather Monitor - 3 Cities with APIs`, `Recipe Cookbook - 5 Dishes`, `Economic Snapshot - 3 Countries`",
	"- RIGHT: `Weather Monitor - Multi-City`, `Recipe Cookbook - Multi-Dish`, `Economic Snapshot - Multi-Country`",
	"- In the `description` and `steps`, use parameterized language like \"for each city\" / \"for N dishes\" / \"for the specified countries\" instead of hardcoding counts.",
	"- If the transcript processed 3 items, the skill should still say \"for each item\" or \"for N items\", not \"for 3 items\".",
	"- This rule applies to BOTH `skills` (new) and `updates` (rewrites). When rewriting an existing skill, also fix its name to the generic form if it currently contains a specific count.",
	"",
	"Update / delete mechanics:",
	"- `updates` entries reuse the exact existing `name` and rewrite the spec from scratch using the current transcript as the source of truth (do not assume the old body's wording).",
	"- Only emit a `deletions` entry when an existing skill is fully obsolete, harmful, or contradicted by the current transcript. The string value must equal the existing skill's exact `name`.",
	"- Never list the same skill name in more than one of `skills`, `updates`, `deletions`.",
	"",
	"Content rules:",
	"- Keep skill names and descriptions scope-accurate. If the transcript only covers part of a broader workflow, name the skill narrowly and mention its limits instead of implying full task-family coverage.",
	"- If you save a skill, include every essential API/tool category, ordering constraint, and output-field requirement that was necessary for successful completion in the transcript.",
	"- Do not omit required steps from the learned skill just because they appeared obvious in context. If a step (e.g. saving the final artifact, calling a finalize tool, validating output) was required, mention it explicitly.",
	"- If nothing is worth saving, set skip_reason and leave skills/updates/deletions empty.",
	"",
	"Failure-aware learning (only when `## Session outcome` is present):",
	"- Treat the outcome status as the ground truth for what the agent actually achieved. Never describe a step the transcript did not actually execute. If the agent did not produce a required artifact, you MUST NOT list \"save the result\" or similar as a completed step.",
	"- On `success` / `partial`: extract the workflow as usual, but mention any partial-credit gaps in `pitfalls` so the next run knows what was missed.",
	"- On `fail` / `agent_error`: prefer a narrowly-scoped skill that captures the lesson. Use the outcome `notes` to focus `pitfalls` on the concrete failure (e.g. wrong endpoint, missing auth header, schema mismatch). Do not pretend the failed step succeeded.",
	"- If the transcript is too thin to extract anything actionable even with the outcome context, set skip_reason instead of inventing content.",
	"",
	"Return strict JSON matching this schema:",
	"{",
	`  "skip_reason": "string or empty",`,
	`  "skills": [{`,
	`    "name": "string",`,
	`    "description": "string",`,
	`    "when_to_use": "string",`,
	`    "steps": ["string"],`,
	`    "pitfalls": ["string"]`,
	"  }],",
	`  "updates": [{`,
	`    "name": "string (must match an existing skill name)",`,
	`    "new_spec": {"name": "string", "description": "string", "when_to_use": "string", "steps": ["string"], "pitfalls": ["string"]},`,
	`    "reason": "string"`,
	"  }],",
	`  "deletions": ["string (must match an existing skill name)"]`,
	"}",
}, "\n")

// DefaultReviewerMessageMaxChars is the default per-message character cap
// applied when rendering the review transcript. Long messages (typically
// large tool results such as raw API payloads) are truncated to head+tail
// to keep the reviewer prompt within the model's context window.
const DefaultReviewerMessageMaxChars = 4000

// LLMReviewer uses a language model to produce ReviewDecisions.
type LLMReviewer struct {
	model           model.Model
	messageMaxChars int
}

// LLMReviewerOption configures an LLMReviewer.
type LLMReviewerOption func(*LLMReviewer)

// WithMessageContentMaxChars caps the rendered character length per
// transcript message. Messages above the cap are truncated to head+tail with
// a placeholder; non-positive values disable truncation.
func WithMessageContentMaxChars(n int) LLMReviewerOption {
	return func(r *LLMReviewer) {
		r.messageMaxChars = n
	}
}

// NewLLMReviewer creates a reviewer backed by the given model.
func NewLLMReviewer(m model.Model, opts ...LLMReviewerOption) *LLMReviewer {
	r := &LLMReviewer{
		model:           m,
		messageMaxChars: DefaultReviewerMessageMaxChars,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Review implements Reviewer by sending the session delta to the model and
// parsing the structured JSON response.
func (r *LLMReviewer) Review(ctx context.Context, input *ReviewInput) (*ReviewDecision, error) {
	input = sanitizeReviewInput(input)
	if input == nil || (len(input.Messages) == 0 && len(input.Transcript) == 0) {
		return nil, nil
	}

	userPrompt := buildUserPrompt(input, r.messageMaxChars)

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: reviewSystemPrompt},
			{Role: model.RoleUser, Content: userPrompt},
		},
	}

	respCh, err := generateReviewerContent(ctx, r.model, req)
	if err != nil {
		return nil, fmt.Errorf("evolution: reviewer generate: %w", err)
	}

	var full strings.Builder
	sawDelta := false
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("evolution: reviewer response: %w", ctx.Err())
		case resp, ok := <-respCh:
			if !ok {
				decision, err := parseReviewDecision(strings.TrimSpace(full.String()))
				if err != nil {
					return nil, err
				}
				return normalizeReviewDecision(decision)
			}
			if resp == nil {
				continue
			}
			if resp.Error != nil {
				return nil, fmt.Errorf("evolution: reviewer response error: %s", resp.Error.Message)
			}
			for _, c := range resp.Choices {
				if c.Delta.Content != "" {
					sawDelta = true
					full.WriteString(c.Delta.Content)
				}
				// Only use Message.Content as a fallback for non-streaming
				// providers. Streaming providers emit both Delta and Message,
				// so appending both would duplicate the text.
				if !sawDelta && c.Message.Content != "" {
					full.WriteString(c.Message.Content)
				}
			}
		}
	}
}

type reviewerGenerateResult struct {
	respCh <-chan *model.Response
	err    error
}

func generateReviewerContent(
	ctx context.Context,
	m model.Model,
	req *model.Request,
) (<-chan *model.Response, error) {
	resultCh := make(chan reviewerGenerateResult, 1)
	go func() {
		respCh, err := m.GenerateContent(ctx, req)
		resultCh <- reviewerGenerateResult{respCh: respCh, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.respCh, result.err
	}
}

func buildUserPrompt(input *ReviewInput, messageMaxChars int) string {
	var b strings.Builder
	b.WriteString("## Session info\n")
	fmt.Fprintf(&b, "- app: %s\n", input.AppName)
	fmt.Fprintf(&b, "- user: %s\n", input.UserID)
	fmt.Fprintf(&b, "- session: %s\n\n", input.SessionID)

	renderOutcome(&b, input.Outcome)

	if len(input.ExistingSkills) > 0 {
		b.WriteString("## Existing skills\n")
		b.WriteString("Each entry shows `name: description` plus a truncated body excerpt. ")
		b.WriteString("Use the excerpt to detect substantive duplicates: if your candidate's workflow ")
		b.WriteString("(steps + when_to_use) overlaps an existing skill except for quantitative parameters ")
		b.WriteString("(counts, sizes, scale levels, named entities), prefer an `updates` entry over a new ")
		b.WriteString("`skills` entry. Use `deletions` only when an existing skill is fully superseded.\n\n")
		for _, s := range input.ExistingSkills {
			renderExistingSkill(&b, s)
		}
		b.WriteString("\n")
	}

	if len(input.Transcript) > 0 {
		b.WriteString("## Transcript (recent delta)\n\n")
		for _, msg := range input.Transcript {
			renderReviewMessage(&b, msg, messageMaxChars)
		}
		return b.String()
	}

	b.WriteString("## Conversation (recent delta)\n\n")
	for _, msg := range input.Messages {
		renderReviewMessage(&b, reviewMessageFromModel(msg), messageMaxChars)
	}
	return b.String()
}

func renderReviewMessage(b *strings.Builder, msg ReviewMessage, maxChars int) {
	roleLabel := string(msg.Role)
	if msg.ToolName != "" {
		roleLabel += " (" + msg.ToolName + ")"
	}
	fmt.Fprintf(b, "### %s\n", roleLabel)
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		text = "(no text)"
	}
	b.WriteString(truncateMessageContent(text, maxChars) + "\n")
	if len(msg.ToolCalls) > 0 {
		b.WriteString("Tool calls:\n")
		for _, call := range msg.ToolCalls {
			fmt.Fprintf(b, "- %s", call.Name)
			args := strings.TrimSpace(call.Arguments)
			if args != "" {
				fmt.Fprintf(b, " args=%s", truncateMessageContent(args, maxChars))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
}

// truncateMessageContent shortens long content (typically raw tool payloads)
// to head+tail with a placeholder describing how many characters were elided.
// Non-positive maxChars disables truncation.
func truncateMessageContent(content string, maxChars int) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	if maxChars < 32 {
		return content[:maxChars]
	}
	headLen := maxChars * 3 / 4
	tailLen := maxChars - headLen
	if tailLen < 16 {
		tailLen = 16
	}
	if headLen+tailLen >= len(content) {
		return content
	}
	omitted := len(content) - headLen - tailLen
	return fmt.Sprintf(
		"%s\n... [%d chars omitted by reviewer transcript truncation] ...\n%s",
		content[:headLen],
		omitted,
		content[len(content)-tailLen:],
	)
}

func messageText(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, p := range msg.ContentParts {
		if p.Text != nil {
			parts = append(parts, *p.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func reviewMessageFromModel(msg model.Message) ReviewMessage {
	return ReviewMessage{
		Role:      msg.Role,
		Content:   messageText(msg),
		ToolName:  msg.ToolName,
		ToolID:    msg.ToolID,
		ToolCalls: reviewToolCallsFromModel(msg.ToolCalls),
	}
}

func reviewToolCallsFromModel(calls []model.ToolCall) []ReviewToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ReviewToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ReviewToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: strings.TrimSpace(string(call.Function.Arguments)),
		})
	}
	return out
}

func stripMarkdownCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if after, ok := strings.CutPrefix(s, "```json"); ok {
		s = after
	} else if after, ok := strings.CutPrefix(s, "```"); ok {
		s = after
	}
	if before, ok := strings.CutSuffix(s, "```"); ok {
		s = before
	}
	return strings.TrimSpace(s)
}

func parseReviewDecision(raw string) (*ReviewDecision, error) {
	var lastErr error
	for _, candidate := range reviewerJSONCandidates(raw) {
		var decision ReviewDecision
		if err := json.Unmarshal([]byte(candidate), &decision); err == nil {
			return &decision, nil
		} else {
			lastErr = err
		}

		repaired, err := jsonrepair.Repair([]byte(candidate))
		if err != nil {
			continue
		}
		if err := json.Unmarshal(repaired, &decision); err == nil {
			return &decision, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("empty reviewer output")
	}
	return nil, fmt.Errorf("evolution: parse reviewer output: %w", lastErr)
}

func reviewerJSONCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	stripped := stripMarkdownCodeFence(raw)
	add(stripped)
	if extracted, ok := extractFirstJSONObject(stripped); ok {
		add(extracted)
	}
	if stripped != raw {
		add(raw)
		if extracted, ok := extractFirstJSONObject(raw); ok {
			add(extracted)
		}
	}
	return candidates
}

func extractFirstJSONObject(text string) (string, bool) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return "", false
}

func normalizeReviewDecision(decision *ReviewDecision) (*ReviewDecision, error) {
	if decision == nil {
		return nil, nil
	}
	decision.SkipReason = strings.TrimSpace(decision.SkipReason)

	// Resolve cross-field name collisions before validating individual lists.
	// Priority: Deletions > Updates > Skills. The first occurrence of a name
	// in this priority order wins; later occurrences are dropped silently.
	claimed := make(map[string]struct{})
	deletions := normalizeDeletions(decision.Deletions, claimed)
	updates, err := normalizeUpdates(decision.Updates, claimed)
	if err != nil {
		return nil, err
	}
	skills, err := normalizeSkills(decision.Skills, claimed)
	if err != nil {
		return nil, err
	}

	if decision.SkipReason != "" &&
		(len(skills) > 0 || len(updates) > 0 || len(deletions) > 0) {
		return nil, fmt.Errorf("evolution: invalid reviewer output: skip_reason cannot coexist with skills, updates, or deletions")
	}

	decision.Skills = skills
	decision.Updates = updates
	decision.Deletions = deletions
	return decision, nil
}

func normalizeDeletions(in []string, claimed map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for _, name := range in {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := claimed[key]; ok {
			continue
		}
		claimed[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizeUpdates(in []*SkillUpdate, claimed map[string]struct{}) ([]*SkillUpdate, error) {
	out := make([]*SkillUpdate, 0, len(in))
	for _, upd := range in {
		normalized, err := normalizeSkillUpdate(upd)
		if err != nil {
			return nil, err
		}
		if normalized == nil {
			continue
		}
		key := strings.ToLower(normalized.Name)
		if _, ok := claimed[key]; ok {
			continue
		}
		claimed[key] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeSkills(in []*SkillSpec, claimed map[string]struct{}) ([]*SkillSpec, error) {
	out := make([]*SkillSpec, 0, len(in))
	for _, spec := range in {
		normalized, err := normalizeSkillSpec(spec)
		if err != nil {
			return nil, err
		}
		if normalized == nil {
			continue
		}
		key := strings.ToLower(normalized.Name)
		if _, ok := claimed[key]; ok {
			continue
		}
		claimed[key] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// normalizeSkillUpdate validates and trims a SkillUpdate. Updates whose
// target name is empty or whose new_spec is nil/incomplete are dropped.
// The new_spec.Name is forced to match the update target so the on-disk
// directory does not move when applied.
func normalizeSkillUpdate(upd *SkillUpdate) (*SkillUpdate, error) {
	if upd == nil {
		return nil, nil
	}
	upd.Name = strings.TrimSpace(upd.Name)
	upd.Reason = strings.TrimSpace(upd.Reason)
	if upd.Name == "" {
		return nil, nil
	}
	if upd.NewSpec == nil {
		return nil, nil
	}
	// Lock the on-disk identity to the update target before validation, so
	// reviewers that omit the redundant new_spec.name field still pass.
	upd.NewSpec.Name = upd.Name
	spec, err := normalizeSkillSpec(upd.NewSpec)
	if err != nil {
		return nil, err
	}
	if spec == nil {
		return nil, nil
	}
	upd.NewSpec = spec
	return upd, nil
}

func normalizeSkillSpec(spec *SkillSpec) (*SkillSpec, error) {
	if spec == nil {
		return nil, nil
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.WhenToUse = strings.TrimSpace(spec.WhenToUse)
	spec.Steps = normalizeStringSlice(spec.Steps)
	spec.Pitfalls = normalizeStringSlice(spec.Pitfalls)
	if spec.Name == "" && spec.Description == "" && spec.WhenToUse == "" && len(spec.Steps) == 0 {
		return nil, nil
	}
	switch {
	case spec.Name == "":
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill name is required")
	case spec.Description == "":
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill description is required")
	case spec.WhenToUse == "":
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill when_to_use is required")
	case len(spec.Steps) == 0:
		return nil, fmt.Errorf("evolution: invalid reviewer output: skill steps are required")
	}
	return spec, nil
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// loadExistingSkills snapshots the repository's skill library into the
// reviewer-facing ExistingSkill shape, optionally including a head
// excerpt of each SKILL.md body.
//
// The body excerpt is what makes the reviewer able to detect substantive
// duplicates ("the proposed workflow already exists, only the dish count
// differs") instead of relying on name/description string matches alone.
// bodyMaxChars caps the per-skill excerpt: a positive value is the
// character budget; zero falls back to the package default;
// a negative value disables bodies entirely (description-only mode).
//
// Errors loading a single skill body are non-fatal — that skill is still
// returned with an empty BodyExcerpt — so an unreadable file never
// silently drops other skills from the reviewer's view.
func loadExistingSkills(repo skill.Repository, bodyMaxChars int) []ExistingSkill {
	if repo == nil {
		return nil
	}
	summaries := repo.Summaries()
	if len(summaries) == 0 {
		return nil
	}
	if bodyMaxChars == 0 {
		bodyMaxChars = defaultExistingSkillBodyMaxChars
	}
	out := make([]ExistingSkill, 0, len(summaries))
	for _, s := range summaries {
		entry := ExistingSkill{
			Name:        s.Name,
			Description: s.Description,
		}
		if bodyMaxChars > 0 {
			if full, err := repo.Get(s.Name); err == nil && full != nil {
				entry.BodyExcerpt = truncateBodyExcerpt(full.Body, bodyMaxChars)
			}
		}
		out = append(out, entry)
	}
	return out
}

// truncateBodyExcerpt cuts a SKILL.md body down to the head budget and
// appends a short ellipsis marker so the reviewer knows the excerpt is
// partial. The marker is intentionally generic ("[truncated]") so it
// does not bias the reviewer toward any particular skill format.
func truncateBodyExcerpt(body string, maxChars int) string {
	body = strings.TrimSpace(body)
	if maxChars <= 0 || len(body) <= maxChars {
		return body
	}
	const marker = "\n... [truncated]"
	cut := maxChars - len(marker)
	if cut < 0 {
		cut = maxChars
	}
	return strings.TrimRight(body[:cut], " \t\r\n") + marker
}

// renderOutcome writes the optional "## Session outcome" block into the
// reviewer prompt. Renders nothing when the caller did not attach an
// Outcome (e.g. online services without an evaluator) so the prompt
// stays identical to the transcript-only baseline in that mode.
func renderOutcome(b *strings.Builder, o *Outcome) {
	if o == nil {
		return
	}
	b.WriteString("## Session outcome\n")
	status := strings.TrimSpace(string(o.Status))
	if status == "" {
		status = "unknown"
	}
	fmt.Fprintf(b, "- status: %s\n", status)
	if o.Score != nil {
		fmt.Fprintf(b, "- score: %.4f\n", *o.Score)
	}
	if eval := strings.TrimSpace(o.Evaluator); eval != "" {
		fmt.Fprintf(b, "- evaluator: %s\n", eval)
	}
	if notes := strings.TrimSpace(o.Notes); notes != "" {
		fmt.Fprintf(b, "- notes: %s\n", notes)
	}
	b.WriteString("\n")
}

// renderExistingSkill writes one existing-skill entry into the reviewer
// prompt. The body excerpt (when present) is rendered as an indented
// fenced block under the name+description so the reviewer can scan the
// list quickly and only "zoom in" when checking for substantive overlap.
func renderExistingSkill(b *strings.Builder, s ExistingSkill) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		name = "(unnamed)"
	}
	desc := strings.TrimSpace(s.Description)
	if desc == "" {
		fmt.Fprintf(b, "- %s\n", name)
	} else {
		fmt.Fprintf(b, "- %s: %s\n", name, desc)
	}
	body := strings.TrimSpace(s.BodyExcerpt)
	if body == "" {
		return
	}
	b.WriteString("  body excerpt:\n")
	for _, line := range strings.Split(body, "\n") {
		fmt.Fprintf(b, "  | %s\n", line)
	}
}
