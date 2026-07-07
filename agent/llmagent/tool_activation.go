//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/toolsnapshot"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolActivationMode controls how an activated tool set is composed.
type ToolActivationMode string

const (
	// ToolActivationModeInclude keeps existing user tools and adds activated tools.
	ToolActivationModeInclude ToolActivationMode = "include"
	// ToolActivationModeOnly keeps only user tools from active only-mode tool sets.
	// It fails closed when no active tool is available. Framework tools remain visible.
	ToolActivationModeOnly ToolActivationMode = "only"
)

// ToolActivationLifetime controls how long a tool activation remains.
type ToolActivationLifetime string

const (
	// ToolActivationLifetimeInvocation keeps activation in the current agent invocation.
	ToolActivationLifetimeInvocation ToolActivationLifetime = "invocation"
	// ToolActivationLifetimeSession keeps activation in session state.
	ToolActivationLifetimeSession ToolActivationLifetime = "session"
)

// ToolActivationOption configures a tool activation rule.
type ToolActivationOption func(*toolActivationRuleOptions)

type toolActivationTriggerKind string

const (
	toolActivationTriggerSkillLoad  toolActivationTriggerKind = "skill_load"
	toolActivationTriggerToolResult toolActivationTriggerKind = "tool_result"
)

type toolActivationTrigger struct {
	kind            toolActivationTriggerKind
	skill           string
	toolName        string
	resultBoolField string
	resultBoolValue bool
	hasResultBool   bool
}

type toolActivationRule struct {
	trigger      toolActivationTrigger
	toolSetNames []string
	mode         ToolActivationMode
	lifetime     ToolActivationLifetime
}

type toolActivationRuleOptions struct {
	mode            ToolActivationMode
	lifetime        ToolActivationLifetime
	resultBoolField string
	resultBoolValue bool
	hasResultBool   bool
}

func newToolActivationRuleOptions(
	opts ...ToolActivationOption,
) toolActivationRuleOptions {
	options := toolActivationRuleOptions{
		mode:     ToolActivationModeInclude,
		lifetime: ToolActivationLifetimeInvocation,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

// WithToolActivationMode sets how activated tool sets are composed.
func WithToolActivationMode(mode ToolActivationMode) ToolActivationOption {
	return func(opts *toolActivationRuleOptions) {
		opts.mode = mode
	}
}

// WithToolActivationLifetime sets how long activation records are retained.
func WithToolActivationLifetime(
	lifetime ToolActivationLifetime,
) ToolActivationOption {
	return func(opts *toolActivationRuleOptions) {
		opts.lifetime = lifetime
	}
}

// WithToolActivationResultJSONBool requires a boolean field in the tool
// result JSON to match before a tool-result activation fires.
func WithToolActivationResultJSONBool(
	field string,
	value bool,
) ToolActivationOption {
	return func(opts *toolActivationRuleOptions) {
		opts.resultBoolField = field
		opts.resultBoolValue = value
		opts.hasResultBool = true
	}
}

type toolActivationRecord struct {
	Mode        ToolActivationMode     `json:"mode"`
	Lifetime    ToolActivationLifetime `json:"lifetime"`
	ToolSetName string                 `json:"tool_set_name"`
}

const (
	toolActivationInvocationStateKey = "tool_activation:records"
	toolActivationSessionPrefix      = "temp:tool_activation:by_agent:"
	toolActivationSegmentDelimiter   = "/"
)

func validateAndNormalizeToolActivationOptions(options *Options) error {
	if options == nil {
		return nil
	}
	toolSetNames, err := collectActivatableToolSetNames(options.activatableToolSets)
	if err != nil {
		return err
	}
	rules, err := normalizeToolActivationRules(
		options.toolActivationRules,
		toolSetNames,
	)
	if err != nil {
		return err
	}
	options.toolActivationRules = rules
	return nil
}

func collectActivatableToolSetNames(
	toolSets []tool.ToolSet,
) (map[string]bool, error) {
	if len(toolSets) == 0 {
		return nil, nil
	}
	names := make(map[string]bool, len(toolSets))
	for _, toolSet := range toolSets {
		if toolSet == nil {
			return nil, fmt.Errorf("activatable tool set must not be nil")
		}
		name := strings.TrimSpace(toolSet.Name())
		if name == "" {
			return nil, fmt.Errorf("activatable tool set name must not be empty")
		}
		if names[name] {
			return nil, fmt.Errorf("duplicate activatable tool set %q", name)
		}
		names[name] = true
	}
	return names, nil
}

func normalizeToolActivationRules(
	rules []toolActivationRule,
	toolSetNames map[string]bool,
) ([]toolActivationRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	out := make([]toolActivationRule, 0, len(rules))
	for _, rule := range rules {
		normalized, err := normalizeToolActivationRule(rule, toolSetNames)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeToolActivationRule(
	rule toolActivationRule,
	toolSetNames map[string]bool,
) (toolActivationRule, error) {
	trigger, err := normalizeToolActivationTrigger(rule.trigger)
	if err != nil {
		return toolActivationRule{}, err
	}
	names := normalizeToolSetNames(rule.toolSetNames)
	if len(names) == 0 {
		return toolActivationRule{}, fmt.Errorf(
			"tool activation for trigger %s must reference at least one tool set",
			trigger.describe(),
		)
	}
	for _, name := range names {
		if !toolSetNames[name] {
			return toolActivationRule{}, fmt.Errorf("unknown activatable tool set %q", name)
		}
	}
	mode, err := normalizeToolActivationMode(rule.mode)
	if err != nil {
		return toolActivationRule{}, err
	}
	lifetime, err := normalizeToolActivationLifetime(rule.lifetime)
	if err != nil {
		return toolActivationRule{}, err
	}
	return toolActivationRule{
		trigger:      trigger,
		toolSetNames: names,
		mode:         mode,
		lifetime:     lifetime,
	}, nil
}

func normalizeToolActivationTrigger(
	trigger toolActivationTrigger,
) (toolActivationTrigger, error) {
	switch trigger.kind {
	case toolActivationTriggerSkillLoad:
		skillName := strings.TrimSpace(trigger.skill)
		if skillName == "" {
			return toolActivationTrigger{}, fmt.Errorf("tool activation skill name must not be empty")
		}
		return toolActivationTrigger{
			kind:  toolActivationTriggerSkillLoad,
			skill: skillName,
		}, nil
	case toolActivationTriggerToolResult:
		toolName := strings.TrimSpace(trigger.toolName)
		if toolName == "" {
			return toolActivationTrigger{}, fmt.Errorf("tool activation tool name must not be empty")
		}
		resultBoolField := strings.TrimSpace(trigger.resultBoolField)
		if trigger.hasResultBool && resultBoolField == "" {
			return toolActivationTrigger{}, fmt.Errorf("tool activation result bool field must not be empty")
		}
		return toolActivationTrigger{
			kind:            toolActivationTriggerToolResult,
			toolName:        toolName,
			resultBoolField: resultBoolField,
			resultBoolValue: trigger.resultBoolValue,
			hasResultBool:   trigger.hasResultBool,
		}, nil
	default:
		return toolActivationTrigger{}, fmt.Errorf(
			"unsupported tool activation trigger %q",
			trigger.kind,
		)
	}
}

func (trigger toolActivationTrigger) describe() string {
	switch trigger.kind {
	case toolActivationTriggerSkillLoad:
		return fmt.Sprintf("%s:%s", trigger.kind, trigger.skill)
	case toolActivationTriggerToolResult:
		if trigger.hasResultBool {
			return fmt.Sprintf(
				"%s:%s:%s=%t",
				trigger.kind,
				trigger.toolName,
				trigger.resultBoolField,
				trigger.resultBoolValue,
			)
		}
		return fmt.Sprintf("%s:%s", trigger.kind, trigger.toolName)
	default:
		return string(trigger.kind)
	}
}

func normalizeToolSetNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func normalizeToolActivationMode(
	mode ToolActivationMode,
) (ToolActivationMode, error) {
	normalized, ok := canonicalToolActivationMode(mode)
	if !ok {
		return "", fmt.Errorf(
			"unsupported tool activation mode %q",
			mode,
		)
	}
	return normalized, nil
}

func normalizeToolActivationLifetime(
	lifetime ToolActivationLifetime,
) (ToolActivationLifetime, error) {
	normalized, ok := canonicalToolActivationLifetime(lifetime)
	if !ok {
		return "", fmt.Errorf(
			"unsupported tool activation lifetime %q",
			lifetime,
		)
	}
	return normalized, nil
}

func canonicalToolActivationMode(
	mode ToolActivationMode,
) (ToolActivationMode, bool) {
	switch mode {
	case ToolActivationModeInclude:
		return ToolActivationModeInclude, true
	case ToolActivationModeOnly:
		return ToolActivationModeOnly, true
	default:
		return "", false
	}
}

func canonicalToolActivationLifetime(
	lifetime ToolActivationLifetime,
) (ToolActivationLifetime, bool) {
	switch lifetime {
	case ToolActivationLifetimeInvocation:
		return ToolActivationLifetimeInvocation, true
	case ToolActivationLifetimeSession:
		return ToolActivationLifetimeSession, true
	default:
		return "", false
	}
}

func (a *LLMAgent) applyToolActivation(
	ctx context.Context,
	inv *agent.Invocation,
	tools []tool.Tool,
	userToolNames map[string]bool,
	externalToolNames map[string]bool,
) ([]tool.Tool, map[string]bool, map[string]bool) {
	toolSets, rules, filter := a.toolActivationInputs()
	return applyToolActivationRecords(
		ctx,
		inv,
		tools,
		userToolNames,
		externalToolNames,
		toolSets,
		rules,
		filter,
	)
}

func (a *LLMAgent) toolActivationInputs() (
	[]tool.ToolSet,
	[]toolActivationRule,
	func(context.Context, tool.Tool) bool,
) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]tool.ToolSet(nil), a.option.activatableToolSets...),
		append([]toolActivationRule(nil), a.option.toolActivationRules...),
		a.option.toolFilter
}

func (a *LLMAgent) handleToolActivationPostToolResult(
	_ context.Context,
	inv *agent.Invocation,
	ev *event.Event,
) {
	if inv == nil || ev == nil {
		return
	}
	var records []toolActivationRecord
	if len(ev.StateDelta) > 0 {
		loaded := loadedSkillsFromStateDelta(inv.AgentName, ev.StateDelta)
		records = append(records, a.activationRecordsForLoadedSkills(loaded)...)
	}
	records = append(records, a.activationRecordsForToolResult(ev)...)
	if len(records) == 0 {
		return
	}
	changed := addInvocationToolActivationRecords(inv, records)
	// Session-lifetime records also need an invocation shadow so the next model request sees them immediately.
	sessionChanged := appendSessionActivationStateDelta(inv, ev, records)
	if changed || sessionChanged {
		toolsnapshot.Invalidate(inv)
	}
}

func loadedSkillsFromStateDelta(
	agentName string,
	delta map[string][]byte,
) map[string]bool {
	prefix := skill.LoadedPrefix(agentName)
	loaded := map[string]bool{}
	for key, value := range delta {
		if !strings.HasPrefix(key, prefix) || len(value) == 0 {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(key, prefix))
		if name == "" {
			continue
		}
		loaded[name] = true
	}
	return loaded
}

func (a *LLMAgent) activationRecordsForLoadedSkills(
	loaded map[string]bool,
) []toolActivationRecord {
	a.mu.RLock()
	rules := append([]toolActivationRule(nil), a.option.toolActivationRules...)
	a.mu.RUnlock()
	records := make([]toolActivationRecord, 0, len(rules))
	for _, rule := range rules {
		if !rule.matchesLoadedSkills(loaded) {
			continue
		}
		for _, toolSetName := range rule.toolSetNames {
			record, ok := newToolActivationRecord(rule.mode, rule.lifetime, toolSetName)
			if ok {
				records = append(records, record)
			}
		}
	}
	return records
}

func (rule toolActivationRule) matchesLoadedSkills(loaded map[string]bool) bool {
	switch rule.trigger.kind {
	case toolActivationTriggerSkillLoad:
		return loaded[rule.trigger.skill]
	default:
		return false
	}
}

func (a *LLMAgent) activationRecordsForToolResult(
	ev *event.Event,
) []toolActivationRecord {
	a.mu.RLock()
	rules := append([]toolActivationRule(nil), a.option.toolActivationRules...)
	a.mu.RUnlock()
	records := make([]toolActivationRecord, 0, len(rules))
	for _, rule := range rules {
		if !rule.matchesToolResult(ev) {
			continue
		}
		for _, toolSetName := range rule.toolSetNames {
			record, ok := newToolActivationRecord(rule.mode, rule.lifetime, toolSetName)
			if ok {
				records = append(records, record)
			}
		}
	}
	return records
}

func (rule toolActivationRule) matchesToolResult(ev *event.Event) bool {
	if rule.trigger.kind != toolActivationTriggerToolResult ||
		ev == nil || ev.Response == nil {
		return false
	}
	for _, choice := range ev.Response.Choices {
		msg := choice.Message
		if strings.TrimSpace(msg.ToolName) != rule.trigger.toolName {
			continue
		}
		if !rule.trigger.hasResultBool {
			return true
		}
		if toolResultBoolFieldMatches(
			msg.Content,
			rule.trigger.resultBoolField,
			rule.trigger.resultBoolValue,
		) {
			return true
		}
	}
	return false
}

func toolResultBoolFieldMatches(content, field string, want bool) bool {
	content = strings.TrimSpace(content)
	field = strings.TrimSpace(field)
	if content == "" || field == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false
	}
	value, ok := payload[field]
	if !ok {
		return false
	}
	got, ok := value.(bool)
	return ok && got == want
}

func appendSessionActivationStateDelta(
	inv *agent.Invocation,
	ev *event.Event,
	records []toolActivationRecord,
) bool {
	changed := false
	for _, record := range records {
		if !record.sessionScoped() {
			continue
		}
		if ev.StateDelta == nil {
			ev.StateDelta = map[string][]byte{}
		}
		key := toolActivationSessionKey(inv.AgentName, record)
		ev.StateDelta[key] = marshalToolActivationRecord(record)
		changed = true
	}
	return changed
}

func newToolActivationRecord(
	mode ToolActivationMode,
	lifetime ToolActivationLifetime,
	toolSetName string,
) (toolActivationRecord, bool) {
	toolSetName = strings.TrimSpace(toolSetName)
	if toolSetName == "" {
		return toolActivationRecord{}, false
	}
	normalizedMode, ok := canonicalToolActivationMode(mode)
	if !ok {
		return toolActivationRecord{}, false
	}
	normalizedLifetime, ok := canonicalToolActivationLifetime(lifetime)
	if !ok {
		return toolActivationRecord{}, false
	}
	return toolActivationRecord{
		Mode:        normalizedMode,
		Lifetime:    normalizedLifetime,
		ToolSetName: toolSetName,
	}, true
}

func (r toolActivationRecord) sessionScoped() bool {
	return r.Lifetime == ToolActivationLifetimeSession
}

func invocationToolActivationRecords(inv *agent.Invocation) []toolActivationRecord {
	records, ok := agent.GetStateValue[[]toolActivationRecord](
		inv,
		toolActivationInvocationStateKey,
	)
	if !ok || len(records) == 0 {
		return nil
	}
	return append([]toolActivationRecord(nil), records...)
}

func addInvocationToolActivationRecords(
	inv *agent.Invocation,
	records []toolActivationRecord,
) bool {
	if inv == nil || len(records) == 0 {
		return false
	}
	base := invocationToolActivationRecords(inv)
	merged := mergeToolActivationRecords(base, records)
	if sameToolActivationRecords(base, merged) {
		return false
	}
	inv.SetState(toolActivationInvocationStateKey, merged)
	return true
}

func toolActivationSessionKey(
	agentName string,
	record toolActivationRecord,
) string {
	return toolActivationSessionPrefixForAgent(agentName) +
		string(record.Mode) +
		toolActivationSegmentDelimiter +
		escapeToolActivationSegment(strings.TrimSpace(record.ToolSetName))
}

func toolActivationSessionPrefixForAgent(agentName string) string {
	agentName = escapeToolActivationSegment(strings.TrimSpace(agentName))
	return toolActivationSessionPrefix + agentName + toolActivationSegmentDelimiter
}

func marshalToolActivationRecord(record toolActivationRecord) []byte {
	b, err := json.Marshal(record)
	if err != nil {
		return nil
	}
	return b
}

func sessionToolActivationRecords(
	ctx context.Context,
	inv *agent.Invocation,
	allowedRecordKeys map[string]bool,
) []toolActivationRecord {
	if inv == nil || inv.Session == nil || len(allowedRecordKeys) == 0 {
		return nil
	}
	state := inv.Session.SnapshotState()
	if len(state) == 0 {
		return nil
	}
	prefix := toolActivationSessionPrefixForAgent(inv.AgentName)
	keys := make([]string, 0, len(state))
	for key := range state {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	records := make([]toolActivationRecord, 0, len(keys))
	for _, key := range keys {
		record, ok := parseSessionToolActivationRecord(
			ctx,
			inv.AgentName,
			key,
			state[key],
		)
		if !ok {
			continue
		}
		if !allowedRecordKeys[toolActivationRecordKey(record)] {
			log.WarnfContext(
				ctx,
				"Disallowed tool activation session record %s",
				key,
			)
			continue
		}
		records = append(records, record)
	}
	return records
}

func mergeToolActivationRecords(
	base []toolActivationRecord,
	added []toolActivationRecord,
) []toolActivationRecord {
	if len(base) == 0 && len(added) == 0 {
		return nil
	}
	out := make([]toolActivationRecord, 0, len(base)+len(added))
	seen := make(map[string]bool, len(base)+len(added))
	for _, record := range append(append([]toolActivationRecord(nil), base...), added...) {
		normalized, ok := newToolActivationRecord(
			record.Mode,
			record.Lifetime,
			record.ToolSetName,
		)
		if !ok {
			continue
		}
		key := toolActivationRecordKey(normalized)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, normalized)
	}
	return out
}

func toolActivationRecordKey(record toolActivationRecord) string {
	return string(record.Mode) + "\x00" +
		string(record.Lifetime) + "\x00" + record.ToolSetName
}

func sameToolActivationRecords(
	a []toolActivationRecord,
	b []toolActivationRecord,
) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if toolActivationRecordKey(a[i]) != toolActivationRecordKey(b[i]) {
			return false
		}
	}
	return true
}

func escapeToolActivationSegment(value string) string {
	return url.PathEscape(value)
}

func parseSessionToolActivationRecord(
	ctx context.Context,
	agentName string,
	key string,
	raw []byte,
) (toolActivationRecord, bool) {
	if len(raw) == 0 {
		return toolActivationRecord{}, false
	}
	var record toolActivationRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		log.WarnfContext(
			ctx,
			"Parse tool activation session record %s failed: %v",
			key,
			err,
		)
		return toolActivationRecord{}, false
	}
	normalized, ok := newToolActivationRecord(
		record.Mode,
		record.Lifetime,
		record.ToolSetName,
	)
	if !ok || !normalized.sessionScoped() {
		log.WarnfContext(
			ctx,
			"Invalid tool activation session record %s",
			key,
		)
		return toolActivationRecord{}, false
	}
	if key != toolActivationSessionKey(agentName, normalized) {
		log.WarnfContext(
			ctx,
			"Mismatched tool activation session record %s",
			key,
		)
		return toolActivationRecord{}, false
	}
	return normalized, ok
}

func applyToolActivationRecords(
	ctx context.Context,
	inv *agent.Invocation,
	tools []tool.Tool,
	userToolNames map[string]bool,
	externalToolNames map[string]bool,
	toolSets []tool.ToolSet,
	rules []toolActivationRule,
	filter func(context.Context, tool.Tool) bool,
) ([]tool.Tool, map[string]bool, map[string]bool) {
	records := mergeToolActivationRecords(
		invocationToolActivationRecords(inv),
		sessionToolActivationRecords(
			ctx,
			inv,
			allowedSessionToolActivationRecordKeys(rules),
		),
	)
	if len(records) == 0 || len(toolSets) == 0 {
		return tools, userToolNames, externalToolNames
	}
	activeSets, onlyNames := activeToolActivationSets(ctx, records, toolSets)
	if len(activeSets) == 0 {
		return tools, userToolNames, externalToolNames
	}
	activatedTools := expandActivatedTools(
		ctx,
		activeSets,
		onlyNames,
		filter,
	)
	if len(activatedTools) == 0 && len(onlyNames) == 0 {
		return tools, userToolNames, externalToolNames
	}
	activatedToolsByName := indexToolActivationTools(activatedTools)
	out, userNames, externalNames := appendActivatedTools(
		ctx,
		tools,
		userToolNames,
		externalToolNames,
		activatedTools,
		activatedToolsByName,
	)
	if len(onlyNames) == 0 {
		return out, userNames, externalNames
	}
	return applyOnly(out, userNames, externalNames, activatedToolsByName)
}

func allowedSessionToolActivationRecordKeys(
	rules []toolActivationRule,
) map[string]bool {
	if len(rules) == 0 {
		return nil
	}
	allowed := make(map[string]bool)
	for _, rule := range rules {
		if rule.lifetime != ToolActivationLifetimeSession {
			continue
		}
		for _, toolSetName := range rule.toolSetNames {
			record, ok := newToolActivationRecord(
				rule.mode,
				rule.lifetime,
				toolSetName,
			)
			if ok {
				allowed[toolActivationRecordKey(record)] = true
			}
		}
	}
	return allowed
}

func activeToolActivationSets(
	ctx context.Context,
	records []toolActivationRecord,
	toolSets []tool.ToolSet,
) ([]tool.ToolSet, map[string]bool) {
	available := make(map[string]bool, len(toolSets))
	for _, toolSet := range toolSets {
		if toolSet == nil {
			continue
		}
		name := strings.TrimSpace(toolSet.Name())
		if name != "" {
			available[name] = true
		}
	}
	activeNames := make(map[string]bool)
	only := make(map[string]bool)
	for _, record := range records {
		name := strings.TrimSpace(record.ToolSetName)
		if !available[name] {
			log.WarnfContext(
				ctx,
				"Unknown activatable tool set: %s",
				record.ToolSetName,
			)
			continue
		}
		activeNames[name] = true
		if record.Mode == ToolActivationModeOnly {
			only[name] = true
		}
	}
	active := make([]tool.ToolSet, 0, len(activeNames))
	seen := make(map[string]bool, len(activeNames))
	for _, toolSet := range toolSets {
		if toolSet == nil {
			continue
		}
		name := strings.TrimSpace(toolSet.Name())
		if !activeNames[name] || seen[name] {
			continue
		}
		seen[name] = true
		active = append(active, toolSet)
	}
	return active, only
}

func expandActivatedTools(
	ctx context.Context,
	active []tool.ToolSet,
	only map[string]bool,
	filter func(context.Context, tool.Tool) bool,
) []tool.Tool {
	out := make([]tool.Tool, 0)
	acceptedToolNames := map[string]bool{}
	for _, toolSet := range active {
		name := strings.TrimSpace(toolSet.Name())
		if len(only) > 0 && !only[name] {
			continue
		}
		tools := expandOneToolActivationSet(
			ctx,
			toolSet,
			acceptedToolNames,
			filter,
		)
		if len(tools) == 0 {
			log.DebugfContext(
				ctx,
				"Activatable tool set %s has no tools",
				name,
			)
		}
		out = append(out, tools...)
	}
	return out
}

func indexToolActivationTools(tools []tool.Tool) map[string]tool.Tool {
	out := make(map[string]tool.Tool, len(tools))
	for _, tl := range tools {
		name := toolActivationToolName(tl)
		if name == "" {
			continue
		}
		if _, ok := out[name]; !ok {
			out[name] = tl
		}
	}
	return out
}

func expandOneToolActivationSet(
	ctx context.Context,
	toolSet tool.ToolSet,
	acceptedToolNames map[string]bool,
	filter func(context.Context, tool.Tool) bool,
) []tool.Tool {
	namedToolSet := itool.NewNamedToolSet(toolSet)
	tools := namedToolSet.Tools(ctx)
	if len(tools) == 0 {
		return nil
	}
	out := make([]tool.Tool, 0, len(tools))
	declaredToolNames := map[string]bool{}
	for _, tl := range tools {
		name := toolActivationToolName(tl)
		if name == "" {
			continue
		}
		if declaredToolNames[name] {
			log.WarnfContext(
				ctx,
				"Duplicate tool name %s in activatable tool set %s",
				name,
				toolSet.Name(),
			)
			continue
		}
		declaredToolNames[name] = true
		if acceptedToolNames[name] {
			log.WarnfContext(
				ctx,
				"Duplicate activated tool name %s across tool sets",
				name,
			)
			continue
		}
		if filter != nil && !filter(ctx, tl) {
			continue
		}
		acceptedToolNames[name] = true
		out = append(out, tl)
	}
	return out
}

func appendActivatedTools(
	ctx context.Context,
	tools []tool.Tool,
	userToolNames map[string]bool,
	externalToolNames map[string]bool,
	activatedTools []tool.Tool,
	activatedToolsByName map[string]tool.Tool,
) ([]tool.Tool, map[string]bool, map[string]bool) {
	out := make([]tool.Tool, 0, len(tools)+len(activatedTools))
	userNames := copyToolActivationNames(userToolNames)
	externalNames := copyToolActivationNames(externalToolNames)
	seen := make(map[string]bool, len(tools)+len(activatedTools))
	for _, tl := range tools {
		name := toolActivationToolName(tl)
		replacement, ok := activatedToolsByName[name]
		if ok && isUserToolActivationName(name, userNames, externalNames) {
			out = append(out, replacement)
			seen[name] = true
			userNames[name] = true
			delete(externalNames, name)
			continue
		}
		if ok && name != "" {
			log.WarnfContext(
				ctx,
				"Activated tool %s conflicts with framework tool; keeping existing tool",
				name,
			)
		}
		if name != "" {
			seen[name] = true
		}
		out = append(out, tl)
	}
	for _, tl := range activatedTools {
		name := toolActivationToolName(tl)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, tl)
		userNames[name] = true
	}
	return out, userNames, externalNames
}

func applyOnly(
	tools []tool.Tool,
	userToolNames map[string]bool,
	externalToolNames map[string]bool,
	allowedToolsByName map[string]tool.Tool,
) ([]tool.Tool, map[string]bool, map[string]bool) {
	out := make([]tool.Tool, 0, len(tools))
	for _, tl := range tools {
		name := toolActivationToolName(tl)
		if name == "" || !isUserToolActivationName(name, userToolNames, externalToolNames) {
			out = append(out, tl)
			continue
		}
		if _, ok := allowedToolsByName[name]; ok {
			out = append(out, tl)
			continue
		}
		delete(userToolNames, name)
		delete(externalToolNames, name)
	}
	return out, userToolNames, externalToolNames
}

func isUserToolActivationName(
	name string,
	userNames map[string]bool,
	externalNames map[string]bool,
) bool {
	return userNames[name] || externalNames[name]
}

func copyToolActivationNames(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for name, ok := range src {
		dst[name] = ok
	}
	return dst
}

func toolActivationToolName(tl tool.Tool) string {
	if tl == nil {
		return ""
	}
	decl := tl.Declaration()
	if decl == nil {
		return ""
	}
	return strings.TrimSpace(decl.Name)
}
