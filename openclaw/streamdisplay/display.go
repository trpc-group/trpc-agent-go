//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package streamdisplay projects stream activity into localized transcript
// snapshots for chat surfaces.
package streamdisplay

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	defaultMaxItems     = 6
	defaultMaxTextRunes = 320
	truncatedSuffix     = "..."
	sectionSep          = "\n\n"
	bulletPrefix        = "- "
	childPrefix         = "  - "

	progressItemID     = "progress"
	publicLanePrefix   = "public"
	reasoningLaneID    = "reasoning"
	toolItemIDPrefix   = "tool:"
	languageChinese    = "zh"
	languageChineseAlt = "cn"
	languageEnglish    = "en"
)

// Language identifies a built-in display language.
type Language string

const (
	// LanguageEnglish renders English labels.
	LanguageEnglish Language = languageEnglish
	// LanguageChinese renders Simplified Chinese labels.
	LanguageChinese Language = languageChinese
)

// ItemKind describes the visible item category.
type ItemKind string

const (
	// ItemKindStatus is a general progress item.
	ItemKindStatus ItemKind = "status"
	// ItemKindReasoning is a reasoning or thought item.
	ItemKindReasoning ItemKind = "reasoning"
	// ItemKindTool is a generic tool item.
	ItemKindTool ItemKind = "tool"
	// ItemKindCommand is a local command item.
	ItemKindCommand ItemKind = "command"
	// ItemKindExplore is a workspace read, list, or search item.
	ItemKindExplore ItemKind = "explore"
	// ItemKindWrite is a workspace write or patch item.
	ItemKindWrite ItemKind = "write"
)

// ItemStatus describes the visible lifecycle of one item.
type ItemStatus string

const (
	// ItemStatusRunning marks an active item.
	ItemStatusRunning ItemStatus = "running"
	// ItemStatusCompleted marks a completed item.
	ItemStatusCompleted ItemStatus = "completed"
	// ItemStatusFailed marks a failed item.
	ItemStatusFailed ItemStatus = "failed"
)

// Labels contains localized display strings.
type Labels struct {
	Working           string
	ThinkingRunning   string
	ThinkingCompleted string
	Calling           string
	Called            string
	Running           string
	Ran               string
	Exploring         string
	Explored          string
	Writing           string
	Wrote             string
	Detail            string
	Answer            string
	Canceled          string
	Ignored           string
	Failed            string
}

// Options controls projection and rendering.
type Options struct {
	Language       string
	Labels         Labels
	ShowReasoning  bool
	MaxItems       int
	MaxTextRunes   int
	HideAnswerHead bool
}

// ToolUpdate describes one tool call lifecycle update.
type ToolUpdate struct {
	ID     string
	Name   string
	Kind   ItemKind
	Status ItemStatus
	Text   string
	Detail string
}

// Projector stores display state for one stream.
type Projector struct {
	opts           Options
	items          []Item
	itemIndex      map[string]int
	answer         strings.Builder
	answerComplete string
	status         string
	errText        string
	done           bool
	canceled       bool
	ignored        bool
	failed         bool
	public         textLane
	reasoning      textLane
}

// Item is one visible transcript item.
type Item struct {
	ID     string
	Kind   ItemKind
	Status ItemStatus
	Name   string
	Text   string
	Detail string
}

// Snapshot is an immutable display view.
type Snapshot struct {
	Items    []Item
	Answer   string
	Status   string
	Error    string
	Done     bool
	Canceled bool
	Ignored  bool
	Failed   bool
	Options  Options
}

type textLane struct {
	prefix   string
	kind     ItemKind
	next     int
	activeID string
	pending  strings.Builder
}

// NewProjector creates a projector with normalized options.
func NewProjector(opts Options) *Projector {
	return &Projector{
		opts:      normalizeOptions(opts),
		itemIndex: make(map[string]int),
		public:    newTextLane(publicLanePrefix, ItemKindStatus),
		reasoning: newTextLane(reasoningLaneID, ItemKindReasoning),
	}
}

// NormalizeLanguage maps locale names and aliases to supported languages.
func NormalizeLanguage(language string) Language {
	normalized := strings.ToLower(strings.TrimSpace(language))
	switch {
	case normalized == "":
		return LanguageEnglish
	case strings.HasPrefix(normalized, languageChinese):
		return LanguageChinese
	case strings.HasPrefix(normalized, languageChineseAlt):
		return LanguageChinese
	case strings.HasPrefix(normalized, languageEnglish):
		return LanguageEnglish
	default:
		return LanguageEnglish
	}
}

// ApplyStatus records a fallback status line for otherwise empty snapshots.
func (p *Projector) ApplyStatus(text string) bool {
	if p == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" || p.status == text {
		return false
	}
	p.status = text
	return true
}

// ApplyProgress records a visible high-level progress item.
func (p *Projector) ApplyProgress(text string) bool {
	if p == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	changed := p.ApplyStatus(text)
	return p.upsertItem(Item{
		ID:     progressItemID,
		Kind:   ItemKindStatus,
		Status: ItemStatusRunning,
		Text:   text,
	}) || changed
}

// ApplyTool records one tool call lifecycle update.
func (p *Projector) ApplyTool(update ToolUpdate) bool {
	if p == nil {
		return false
	}
	item := toolItem(update)
	if item.ID == "" {
		return false
	}
	return p.upsertItem(item)
}

// ApplyPublicDelta appends incremental public progress text.
func (p *Projector) ApplyPublicDelta(delta string) bool {
	if p == nil {
		return false
	}
	return p.applyLaneDelta(&p.public, delta)
}

// ApplyPublicCompleted commits public progress text as a completed item.
func (p *Projector) ApplyPublicCompleted(text string) bool {
	if p == nil {
		return false
	}
	return p.applyLaneCompleted(&p.public, text)
}

// ApplyReasoningDelta appends incremental reasoning text when enabled.
func (p *Projector) ApplyReasoningDelta(delta string) bool {
	if p == nil || !p.opts.ShowReasoning {
		return false
	}
	return p.applyLaneDelta(&p.reasoning, delta)
}

// ApplyReasoningCompleted commits reasoning text when enabled.
func (p *Projector) ApplyReasoningCompleted(text string) bool {
	if p == nil || !p.opts.ShowReasoning {
		return false
	}
	return p.applyLaneCompleted(&p.reasoning, text)
}

// ApplyAnswerDelta appends incremental answer text.
func (p *Projector) ApplyAnswerDelta(delta string) bool {
	if p == nil || delta == "" {
		return false
	}
	p.answer.WriteString(delta)
	return true
}

// ApplyAnswerCompleted records the final answer text.
func (p *Projector) ApplyAnswerCompleted(text string) bool {
	if p == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" || p.answerComplete == text {
		return false
	}
	p.answerComplete = text
	return true
}

// Complete marks the stream as successfully completed.
func (p *Projector) Complete() bool {
	if p == nil || p.done {
		return false
	}
	p.done = true
	return true
}

// Cancel marks the stream as canceled.
func (p *Projector) Cancel() bool {
	if p == nil {
		return false
	}
	changed := !p.done || !p.canceled
	p.done = true
	p.canceled = true
	return changed
}

// Ignore marks the stream as ignored.
func (p *Projector) Ignore() bool {
	if p == nil {
		return false
	}
	changed := !p.done || !p.ignored
	p.done = true
	p.ignored = true
	return changed
}

// Fail marks the stream as failed.
func (p *Projector) Fail(message string) bool {
	if p == nil {
		return false
	}
	message = strings.TrimSpace(message)
	changed := !p.done || p.errText != message
	p.done = true
	p.failed = true
	p.errText = message
	return changed
}

// Snapshot returns the current display snapshot.
func (p *Projector) Snapshot() Snapshot {
	if p == nil {
		return Snapshot{Options: normalizeOptions(Options{})}
	}
	items := make([]Item, len(p.items))
	copy(items, p.items)
	answer := p.answerComplete
	if answer == "" {
		answer = p.answer.String()
	}
	return Snapshot{
		Items:    items,
		Answer:   strings.TrimSpace(answer),
		Status:   strings.TrimSpace(p.status),
		Error:    strings.TrimSpace(p.errText),
		Done:     p.done,
		Canceled: p.canceled,
		Ignored:  p.ignored,
		Failed:   p.failed,
		Options:  p.opts,
	}
}

// HasContent reports whether the projector currently renders visible text.
func (p *Projector) HasContent() bool {
	if p == nil {
		return false
	}
	return Render(p.Snapshot()) != ""
}

// Render renders a snapshot into Markdown-like plain text.
func Render(snapshot Snapshot) string {
	opts := normalizeOptions(snapshot.Options)
	labels := opts.Labels
	maxTextRunes := opts.MaxTextRunes

	parts := make([]string, 0, len(snapshot.Items)+2)
	for _, item := range limitedItems(snapshot.Items, opts.MaxItems) {
		rendered := renderItem(item, labels, maxTextRunes)
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}

	if snapshot.Failed || snapshot.Error != "" {
		parts = append(parts, errorText(labels, snapshot.Error, maxTextRunes))
	} else if snapshot.Canceled {
		parts = append(parts, labels.Canceled)
	} else if snapshot.Ignored {
		parts = append(parts, labels.Ignored)
	}

	answer := strings.TrimSpace(snapshot.Answer)
	if answer != "" {
		answer = truncateRunes(answer, maxTextRunes)
		if !opts.HideAnswerHead {
			answer = labels.Answer + "\n" + answer
		}
		parts = append(parts, answer)
	} else if snapshot.Status != "" && len(parts) == 0 {
		parts = append(
			parts,
			labels.Working+": "+
				truncateRunes(snapshot.Status, maxTextRunes),
		)
	}

	return strings.TrimSpace(strings.Join(parts, sectionSep))
}

func normalizeOptions(opts Options) Options {
	if opts.MaxItems <= 0 {
		opts.MaxItems = defaultMaxItems
	}
	if opts.MaxTextRunes <= 0 {
		opts.MaxTextRunes = defaultMaxTextRunes
	}
	opts.Labels = mergeLabels(
		baseLabelsForLanguage(opts.Language),
		opts.Labels,
	)
	return opts
}

func baseLabelsForLanguage(language string) Labels {
	switch NormalizeLanguage(language) {
	case LanguageChinese:
		return chineseLabels()
	default:
		return englishLabels()
	}
}

func mergeLabels(base Labels, override Labels) Labels {
	if override.Working != "" {
		base.Working = override.Working
	}
	if override.ThinkingRunning != "" {
		base.ThinkingRunning = override.ThinkingRunning
	}
	if override.ThinkingCompleted != "" {
		base.ThinkingCompleted = override.ThinkingCompleted
	}
	if override.Calling != "" {
		base.Calling = override.Calling
	}
	if override.Called != "" {
		base.Called = override.Called
	}
	if override.Running != "" {
		base.Running = override.Running
	}
	if override.Ran != "" {
		base.Ran = override.Ran
	}
	if override.Exploring != "" {
		base.Exploring = override.Exploring
	}
	if override.Explored != "" {
		base.Explored = override.Explored
	}
	if override.Writing != "" {
		base.Writing = override.Writing
	}
	if override.Wrote != "" {
		base.Wrote = override.Wrote
	}
	if override.Detail != "" {
		base.Detail = override.Detail
	}
	if override.Answer != "" {
		base.Answer = override.Answer
	}
	if override.Canceled != "" {
		base.Canceled = override.Canceled
	}
	if override.Ignored != "" {
		base.Ignored = override.Ignored
	}
	if override.Failed != "" {
		base.Failed = override.Failed
	}
	return base
}

func englishLabels() Labels {
	return Labels{
		Working:           "Working",
		ThinkingRunning:   "Thinking",
		ThinkingCompleted: "Thought",
		Calling:           "Calling",
		Called:            "Called",
		Running:           "Running",
		Ran:               "Ran",
		Exploring:         "Exploring",
		Explored:          "Explored",
		Writing:           "Writing",
		Wrote:             "Wrote",
		Detail:            "Args",
		Answer:            "Answer",
		Canceled:          "Canceled",
		Ignored:           "Ignored",
		Failed:            "Failed",
	}
}

func chineseLabels() Labels {
	return Labels{
		Working:           "处理中",
		ThinkingRunning:   "思考中",
		ThinkingCompleted: "思考",
		Calling:           "正在调用",
		Called:            "已调用",
		Running:           "正在执行",
		Ran:               "已执行",
		Exploring:         "正在查看",
		Explored:          "已查看",
		Writing:           "正在写入",
		Wrote:             "已写入",
		Detail:            "参数",
		Answer:            "回答",
		Canceled:          "已取消",
		Ignored:           "已忽略",
		Failed:            "失败",
	}
}

func newTextLane(prefix string, kind ItemKind) textLane {
	return textLane{
		prefix: prefix,
		kind:   kind,
	}
}

func (p *Projector) applyLaneDelta(
	lane *textLane,
	delta string,
) bool {
	if lane == nil || delta == "" {
		return false
	}
	lane.pending.WriteString(delta)
	if strings.TrimSpace(lane.pending.String()) == "" {
		return false
	}
	return p.upsertItem(Item{
		ID:     lane.ensureActiveID(),
		Kind:   lane.kind,
		Status: ItemStatusRunning,
		Text:   lane.pending.String(),
	})
}

func (p *Projector) applyLaneCompleted(
	lane *textLane,
	text string,
) bool {
	if lane == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = strings.TrimSpace(lane.pending.String())
	}
	if text == "" {
		return false
	}
	changed := p.upsertItem(Item{
		ID:     lane.ensureActiveID(),
		Kind:   lane.kind,
		Status: ItemStatusCompleted,
		Text:   text,
	})
	lane.clearActive()
	return changed
}

func (l *textLane) ensureActiveID() string {
	if l.activeID != "" {
		return l.activeID
	}
	l.next++
	l.activeID = l.prefix + ":" + strconv.Itoa(l.next)
	return l.activeID
}

func (l *textLane) clearActive() {
	l.activeID = ""
	l.pending.Reset()
}

func toolItem(update ToolUpdate) Item {
	name := strings.TrimSpace(update.Name)
	id := strings.TrimSpace(update.ID)
	if id == "" && name != "" {
		id = toolItemIDPrefix + name
	}
	return Item{
		ID:     id,
		Kind:   normalizeItemKind(update.Kind),
		Status: normalizeItemStatus(update.Status),
		Name:   name,
		Text:   strings.TrimSpace(update.Text),
		Detail: strings.TrimSpace(update.Detail),
	}
}

func normalizeItemKind(kind ItemKind) ItemKind {
	switch kind {
	case ItemKindStatus,
		ItemKindReasoning,
		ItemKindTool,
		ItemKindCommand,
		ItemKindExplore,
		ItemKindWrite:
		return kind
	default:
		return ItemKindTool
	}
}

func normalizeItemStatus(status ItemStatus) ItemStatus {
	switch status {
	case ItemStatusRunning,
		ItemStatusCompleted,
		ItemStatusFailed:
		return status
	default:
		return ItemStatusRunning
	}
}

func (p *Projector) upsertItem(item Item) bool {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		return false
	}
	if idx, ok := p.itemIndex[item.ID]; ok {
		return updateItem(&p.items[idx], item)
	}
	p.itemIndex[item.ID] = len(p.items)
	p.items = append(p.items, item)
	return true
}

func updateItem(existing *Item, next Item) bool {
	if existing == nil {
		return false
	}
	changed := false
	if next.Kind != "" && existing.Kind != next.Kind {
		existing.Kind = next.Kind
		changed = true
	}
	if next.Status != "" && existing.Status != next.Status {
		existing.Status = next.Status
		changed = true
	}
	if next.Name != "" && existing.Name != next.Name {
		existing.Name = next.Name
		changed = true
	}
	if next.Text != "" && existing.Text != next.Text {
		existing.Text = next.Text
		changed = true
	}
	if next.Detail != "" && existing.Detail != next.Detail {
		existing.Detail = next.Detail
		changed = true
	}
	return changed
}

func limitedItems(items []Item, limit int) []Item {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}

func renderItem(item Item, labels Labels, maxTextRunes int) string {
	head := itemHead(item, labels)
	if head == "" {
		return ""
	}
	text := truncateRunes(strings.TrimSpace(item.Text), maxTextRunes)
	detail := truncateRunes(strings.TrimSpace(item.Detail), maxTextRunes)
	lines := []string{bulletPrefix + head}
	if detail != "" {
		lines = append(lines, childPrefix+labels.Detail+": "+detail)
	}
	if text != "" && text != item.Name && text != detail {
		lines = append(lines, childPrefix+text)
	}
	return strings.Join(lines, "\n")
}

func itemHead(item Item, labels Labels) string {
	if item.Status == ItemStatusFailed {
		return namedHead(labels.Failed, item.Name)
	}
	switch item.Kind {
	case ItemKindReasoning:
		if item.Status == ItemStatusCompleted {
			return labels.ThinkingCompleted
		}
		return labels.ThinkingRunning
	case ItemKindCommand:
		return lifecycleHead(item, labels.Running, labels.Ran)
	case ItemKindExplore:
		return lifecycleHead(item, labels.Exploring, labels.Explored)
	case ItemKindWrite:
		return lifecycleHead(item, labels.Writing, labels.Wrote)
	case ItemKindStatus:
		return labels.Working
	default:
		return lifecycleHead(item, labels.Calling, labels.Called)
	}
}

func lifecycleHead(item Item, running, completed string) string {
	if item.Status == ItemStatusCompleted {
		return namedHead(completed, item.Name)
	}
	return namedHead(running, item.Name)
}

func namedHead(label, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return label
	}
	return label + " " + name
}

func errorText(labels Labels, text string, maxTextRunes int) string {
	text = truncateRunes(text, maxTextRunes)
	if text == "" {
		return labels.Failed
	}
	return labels.Failed + ": " + text
}

func truncateRunes(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	suffixRunes := []rune(truncatedSuffix)
	if maxRunes <= len(suffixRunes) {
		return string(runes[:maxRunes])
	}
	return strings.TrimSpace(
		string(runes[:maxRunes-len(suffixRunes)]),
	) + truncatedSuffix
}
