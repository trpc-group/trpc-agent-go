//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolsearch provides a runtime DeferredToolSet that exposes a local
// lexical `tool_search` function plus only the loaded tool schemas needed for
// the next model step.
package toolsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/invocationcarrier"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	invocationStateKeyPrefix = "__deferred_toolset_state__:"

	// SessionStateKeyPrefix prefixes session-state keys used to persist
	// deferred tool-search loaded-tool mirrors.
	SessionStateKeyPrefix = "toolsearch:"
)

type loadedState struct {
	CatalogFingerprint string   `json:"catalog_fingerprint,omitempty"`
	LoadedTools        []string `json:"loaded_tools,omitempty"`
}

type catalogSnapshot struct {
	BuiltAt     time.Time
	Fingerprint string
	Entries     []catalogEntry
	ByName      map[string]catalogEntry
	Index       *localIndex
}

// DeferredToolSet is a ToolSet that dynamically exposes a search tool plus
// only the loaded tools needed for the next model step.
type DeferredToolSet struct {
	name                string
	searchToolName      string
	searchToolDesc      string
	stateNamespace      string
	maxResults          int
	maxLoaded           int
	alwaysInclude       []string
	refreshPolicy       CatalogRefreshPolicy
	stateScope          StateScope
	analyzer            Analyzer
	directTools         []tool.Tool
	toolSets            []tool.ToolSet
	manageToolSetCloser bool

	mu       sync.RWMutex
	snapshot *catalogSnapshot
	expires  time.Time

	searchTool *searchTool
}

// NewDeferredToolSet creates a step-dynamic ToolSet that exposes a single
// lexical search tool plus only the loaded tools needed for later model steps.
func NewDeferredToolSet(opts ...Option) (*DeferredToolSet, error) {
	cfg := &config{
		searchToolName: defaultSearchToolName,
		maxResults:     defaultMaxResults,
		maxLoaded:      defaultMaxLoaded,
		refreshPolicy: CatalogRefreshPolicy{
			TTL: defaultCatalogTTL,
		},
		stateScope: StateScopeInvocation,
		analyzer:   DefaultAnalyzer(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	if cfg.searchToolName == "" {
		cfg.searchToolName = defaultSearchToolName
	}
	if cfg.maxResults <= 0 {
		cfg.maxResults = defaultMaxResults
	}
	if cfg.maxLoaded <= 0 {
		cfg.maxLoaded = defaultMaxLoaded
	}
	if cfg.analyzer == nil {
		cfg.analyzer = DefaultAnalyzer()
	}
	if len(cfg.directTools) == 0 && len(cfg.toolSets) == 0 {
		return nil, fmt.Errorf("newing deferred tool set: no tools or toolsets configured")
	}
	alwaysInclude := make([]string, 0, len(cfg.alwaysInclude))
	seen := make(map[string]struct{}, len(cfg.alwaysInclude))
	for _, name := range cfg.alwaysInclude {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		alwaysInclude = append(alwaysInclude, name)
		seen[name] = struct{}{}
	}
	namespace := strings.TrimSpace(cfg.stateNamespace)
	if namespace == "" {
		namespace = cfg.searchToolName
	}
	set := &DeferredToolSet{
		name:                strings.TrimSpace(cfg.name),
		searchToolName:      cfg.searchToolName,
		searchToolDesc:      cfg.searchToolDesc,
		stateNamespace:      namespace,
		maxResults:          cfg.maxResults,
		maxLoaded:           cfg.maxLoaded,
		alwaysInclude:       alwaysInclude,
		refreshPolicy:       cfg.refreshPolicy,
		stateScope:          cfg.stateScope,
		analyzer:            cfg.analyzer,
		directTools:         append([]tool.Tool(nil), cfg.directTools...),
		toolSets:            append([]tool.ToolSet(nil), cfg.toolSets...),
		manageToolSetCloser: cfg.manageToolSetCloser,
	}
	set.searchTool = newSearchTool(set)
	return set, nil
}

// Name implements tool.ToolSet.
func (d *DeferredToolSet) Name() string {
	if d == nil {
		return ""
	}
	return d.name
}

// Close implements tool.ToolSet.
func (d *DeferredToolSet) Close() error {
	if d == nil || !d.manageToolSetCloser {
		return nil
	}
	var firstErr error
	for _, ts := range d.toolSets {
		if ts == nil {
			continue
		}
		if err := ts.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// StepDynamic marks DeferredToolSet as a step-dynamic ToolSet.
func (d *DeferredToolSet) StepDynamic() bool {
	return true
}

// Tools implements tool.ToolSet.
func (d *DeferredToolSet) Tools(ctx context.Context) []tool.Tool {
	if d == nil {
		return nil
	}
	snapshot := d.catalogSnapshot(ctx)
	loaded := d.loadState(ctx, snapshot)
	names := make([]string, 0, 1+len(d.alwaysInclude)+len(loaded.LoadedTools))
	seen := make(map[string]struct{}, 1+len(d.alwaysInclude)+len(loaded.LoadedTools))
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		names = append(names, name)
		seen[name] = struct{}{}
	}
	appendName(d.searchToolName)
	for _, name := range d.alwaysInclude {
		if _, ok := snapshot.ByName[name]; ok {
			appendName(name)
		}
	}
	for _, name := range loaded.LoadedTools {
		if _, ok := snapshot.ByName[name]; ok {
			appendName(name)
		}
	}
	toolsByName := make(map[string]tool.Tool, len(snapshot.ByName)+1)
	toolsByName[d.searchToolName] = d.searchTool
	for name, entry := range snapshot.ByName {
		toolsByName[name] = entry.Tool
	}
	out := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		if tl := toolsByName[name]; tl != nil {
			out = append(out, tl)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Declaration().Name < out[j].Declaration().Name
	})
	return out
}

// LoadedToolNames returns the currently loaded deferred tool names visible from
// the invocation or session state carried by ctx.
func (d *DeferredToolSet) LoadedToolNames(ctx context.Context) []string {
	if d == nil {
		return nil
	}
	snapshot := d.catalogSnapshot(ctx)
	state := d.loadState(ctx, snapshot)
	return append([]string(nil), state.LoadedTools...)
}

// ClearSessionLoadedTools clears only the persisted session mirror for loaded
// tools. It intentionally leaves invocation state untouched so the current
// model/tool loop can continue using tools that were already loaded.
func (d *DeferredToolSet) ClearSessionLoadedTools(ctx context.Context) bool {
	if d == nil || d.stateScope != StateScopeSession {
		return false
	}
	inv := stateCarrierInvocation(ctx)
	if inv == nil || inv.Session == nil {
		return false
	}
	key := d.sessionStateKey()
	if raw, ok := inv.Session.GetState(key); !ok || len(raw) == 0 {
		return false
	}
	inv.Session.SetState(key, nil)
	return true
}

// SessionStateKey returns the session-state key used for the loaded-tool mirror.
func (d *DeferredToolSet) SessionStateKey() string {
	if d == nil {
		return ""
	}
	return d.sessionStateKey()
}

func (d *DeferredToolSet) catalogSnapshot(ctx context.Context) *catalogSnapshot {
	now := time.Now()
	snapshot := d.baseCatalogSnapshot(ctx, now)
	if filter := invocationToolFilter(ctx); filter != nil {
		return d.filteredCatalogSnapshot(ctx, snapshot, filter)
	}
	return snapshot
}

func (d *DeferredToolSet) baseCatalogSnapshot(
	ctx context.Context,
	now time.Time,
) *catalogSnapshot {
	d.mu.RLock()
	if d.snapshot != nil &&
		d.refreshPolicy.TTL > 0 &&
		now.Before(d.expires) {
		snapshot := d.snapshot
		d.mu.RUnlock()
		return snapshot
	}
	d.mu.RUnlock()

	snapshot := d.buildCatalogSnapshot(ctx, now)

	d.mu.Lock()
	d.snapshot = snapshot
	if d.refreshPolicy.TTL > 0 {
		d.expires = now.Add(d.refreshPolicy.TTL)
	} else {
		d.expires = time.Time{}
	}
	d.mu.Unlock()
	return snapshot
}

func (d *DeferredToolSet) filteredCatalogSnapshot(
	ctx context.Context,
	base *catalogSnapshot,
	filter tool.FilterFunc,
) *catalogSnapshot {
	if base == nil || filter == nil {
		return base
	}
	entries := make([]catalogEntry, 0, len(base.Entries))
	entriesByName := make(map[string]catalogEntry, len(base.ByName))
	for _, entry := range base.Entries {
		if entry.Tool == nil || !filter(ctx, entry.Tool) {
			continue
		}
		entries = append(entries, entry)
		entriesByName[entry.Name] = entry
	}
	return &catalogSnapshot{
		BuiltAt:     base.BuiltAt,
		Fingerprint: catalogFingerprint(entries),
		Entries:     entries,
		ByName:      entriesByName,
		Index:       newLocalIndex(entries, d.analyzer),
	}
}

func (d *DeferredToolSet) buildCatalogSnapshot(
	ctx context.Context,
	now time.Time,
) *catalogSnapshot {
	entriesByName := make(map[string]catalogEntry, len(d.directTools)+8)
	addEntry := func(tl tool.Tool, bucket string) {
		if tl == nil || tl.Declaration() == nil {
			return
		}
		decl := tl.Declaration()
		if decl.Name == d.searchToolName {
			log.WarnfContext(
				ctx,
				"deferred tool set skips source tool %q because it conflicts with search tool name",
				decl.Name,
			)
			return
		}
		entriesByName[decl.Name] = catalogEntry{
			Name:        decl.Name,
			Description: strings.TrimSpace(decl.Description),
			SearchText:  buildSearchText(bucket, decl),
			LimitBucket: strings.TrimSpace(bucket),
			Tool:        tl,
		}
	}
	for _, tl := range d.directTools {
		addEntry(tl, "")
	}
	for _, ts := range d.toolSets {
		if ts == nil {
			continue
		}
		named := itool.NewNamedToolSet(ts)
		bucket := strings.TrimSpace(ts.Name())
		for _, tl := range named.Tools(ctx) {
			addEntry(tl, bucket)
		}
	}
	names := make([]string, 0, len(entriesByName))
	for name := range entriesByName {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]catalogEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, entriesByName[name])
	}
	return &catalogSnapshot{
		BuiltAt:     now,
		Fingerprint: catalogFingerprint(entries),
		Entries:     entries,
		ByName:      entriesByName,
		Index:       newLocalIndex(entries, d.analyzer),
	}
}

func buildSearchText(bucket string, decl *tool.Declaration) string {
	if decl == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	if name := strings.TrimSpace(decl.Name); name != "" {
		parts = append(parts, name)
		parts = append(parts, strings.ReplaceAll(name, "_", " "))
		parts = append(parts, strings.ReplaceAll(name, "-", " "))
	}
	if bucket = strings.TrimSpace(bucket); bucket != "" {
		parts = append(parts, bucket)
	}
	if desc := strings.TrimSpace(decl.Description); desc != "" {
		parts = append(parts, desc)
	}
	parts = append(parts, schemaPropertyNames(decl.InputSchema)...)
	return strings.Join(parts, " ")
}

func schemaPropertyNames(schema *tool.Schema) []string {
	if schema == nil || len(schema.Properties) == 0 {
		return nil
	}
	names := make([]string, 0, len(schema.Properties))
	for name, child := range schema.Properties {
		names = append(names, name)
		if child != nil && len(child.Properties) > 0 {
			names = append(names, schemaPropertyNames(child)...)
		}
	}
	sort.Strings(names)
	return names
}

func catalogFingerprint(entries []catalogEntry) string {
	if len(entries) == 0 {
		return "0"
	}
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(entry.Name)
		b.WriteString("\x00")
		b.WriteString(entry.Description)
		b.WriteString("\x00")
		b.WriteString(entry.LimitBucket)
		b.WriteString("\x00")
	}
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(b.String())))
}

func (d *DeferredToolSet) searchToolDescription() string {
	if strings.TrimSpace(d.searchToolDesc) != "" {
		return d.searchToolDesc
	}
	return "Search the deferred tool catalog and load the relevant tool definitions for the next model step."
}

func invocationToolFilter(ctx context.Context) tool.FilterFunc {
	if ctx == nil {
		return nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	return inv.RunOptions.ToolFilter
}

func invocationStateCarrier(inv *agent.Invocation) *agent.Invocation {
	if inv == nil {
		return nil
	}
	carrier := inv
	for parent := carrier.GetParentInvocation(); parent != nil; parent = parent.GetParentInvocation() {
		if parent.Agent != carrier.Agent || parent.AgentName != carrier.AgentName {
			break
		}
		carrier = parent
	}
	return carrier
}

func stateCarrierInvocation(ctx context.Context) *agent.Invocation {
	if carrier, ok := invocationcarrier.InvocationStateCarrierFromContext(ctx); ok {
		return carrier
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok {
		return nil
	}
	return invocationStateCarrier(inv)
}

func (d *DeferredToolSet) loadState(
	ctx context.Context,
	snapshot *catalogSnapshot,
) loadedState {
	state := loadedState{CatalogFingerprint: snapshot.Fingerprint}
	loadedFromSession := false
	if inv := stateCarrierInvocation(ctx); inv != nil {
		if v, ok := agent.GetStateValue[loadedState](inv, d.invocationStateKey()); ok {
			state = v
		}
	}
	if len(state.LoadedTools) == 0 {
		if inv := stateCarrierInvocation(ctx); inv != nil && inv.Session != nil {
			state, loadedFromSession = d.loadSessionMirror(inv, state)
		}
	}
	state = d.normalizeLoadedState(snapshot, state)
	if inv := stateCarrierInvocation(ctx); inv != nil {
		inv.SetState(d.invocationStateKey(), state)
		if loadedFromSession {
			d.storeSessionMirror(inv, state)
		}
	}
	return state
}

func (d *DeferredToolSet) normalizeLoadedState(
	snapshot *catalogSnapshot,
	state loadedState,
) loadedState {
	if snapshot == nil {
		return loadedState{}
	}
	state.CatalogFingerprint = snapshot.Fingerprint
	if len(state.LoadedTools) == 0 {
		state.LoadedTools = nil
		return state
	}
	out := make([]string, 0, len(state.LoadedTools))
	seen := make(map[string]struct{}, len(state.LoadedTools))
	for _, name := range state.LoadedTools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := snapshot.ByName[name]; !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, name)
		seen[name] = struct{}{}
	}
	if d.maxLoaded > 0 && len(out) > d.maxLoaded {
		out = append([]string(nil), out[len(out)-d.maxLoaded:]...)
	}
	state.LoadedTools = out
	return state
}

func (d *DeferredToolSet) updateLoadedState(
	ctx context.Context,
	snapshot *catalogSnapshot,
	additional []string,
) loadedState {
	state := d.loadState(ctx, snapshot)
	if len(additional) == 0 {
		return state
	}
	for _, name := range additional {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		state.LoadedTools = appendIfMissing(state.LoadedTools, name)
	}
	state = d.normalizeLoadedState(snapshot, state)
	if inv := stateCarrierInvocation(ctx); inv != nil {
		inv.SetState(d.invocationStateKey(), state)
		d.storeSessionMirror(inv, state)
	}
	return state
}

func appendIfMissing(names []string, name string) []string {
	for _, existing := range names {
		if existing == name {
			return names
		}
	}
	return append(names, name)
}

func (d *DeferredToolSet) invocationStateKey() string {
	return invocationStateKeyPrefix + d.stateNamespace
}

func (d *DeferredToolSet) sessionStateKey() string {
	return SessionStateKeyPrefix + d.stateNamespace
}

func (d *DeferredToolSet) loadSessionMirror(
	inv *agent.Invocation,
	fallback loadedState,
) (loadedState, bool) {
	if inv == nil || inv.Session == nil || d.stateScope != StateScopeSession {
		return fallback, false
	}
	raw, ok := inv.Session.GetState(d.sessionStateKey())
	if !ok || len(raw) == 0 {
		return fallback, false
	}
	var stored loadedState
	if err := json.Unmarshal(raw, &stored); err == nil {
		return stored, true
	}
	return fallback, false
}

func (d *DeferredToolSet) storeSessionMirror(
	inv *agent.Invocation,
	state loadedState,
) {
	if inv == nil || inv.Session == nil || d.stateScope != StateScopeSession {
		return
	}
	key := d.sessionStateKey()
	if len(state.LoadedTools) == 0 {
		inv.Session.SetState(key, nil)
		return
	}
	b, err := json.Marshal(state)
	if err != nil {
		return
	}
	inv.Session.SetState(key, b)
}
