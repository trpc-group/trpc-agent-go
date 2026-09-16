//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var _ plugin.Plugin = (*contextOffloadPlugin)(nil)

const (
	contextOffloadPluginName  = "tencentdb_context_offload"
	defaultModelContextWindow = 128000
	maxOffloadPromptRunes     = 500
	maxOffloadRecentRunes     = 400
	maxOffloadRecentMessages  = 10
	maxOffloadPromptSessions  = 1024
	heartbeatPromptPrefix     = "Read HEARTBEAT.md if it exists"
	heartbeatReply            = "HEARTBEAT_OK"
)

// ContextOffloadPlugin returns a runner plugin that delegates short-term
// context offload to the TencentDB Agent Memory v2 API. It is separate from
// Plugin so long-term recall does not unexpectedly rewrite tool history.
func (s *Service) ContextOffloadPlugin() plugin.Plugin {
	if s == nil {
		return &contextOffloadPlugin{opts: defaultOptions()}
	}
	return &contextOffloadPlugin{
		opts:   s.opts,
		client: s.contextOffloadClient(),
	}
}

// NewContextOffloadPlugin creates a standalone context offload plugin.
//
// Configuration errors are reported through logs when a hook first runs.
// NewService is preferred when callers need eager configuration validation and
// the companion result-reference reader tool.
func NewContextOffloadPlugin(opts ...Option) plugin.Plugin {
	options := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	options.ContextOffload = normalizeContextOffloadConfig(
		options.ContextOffload,
	)
	return &contextOffloadPlugin{opts: options}
}

type offloadPromptKey struct {
	sessionID string
	prompt    string
}

type contextOffloadPlugin struct {
	opts   Options
	client *offloadGatewayClient

	promptMu        sync.Mutex
	lastPrompt      map[string]string
	promptOrder     []string
	promptsInFlight map[offloadPromptKey]struct{}
}

func (p *contextOffloadPlugin) Name() string {
	return contextOffloadPluginName
}

func (p *contextOffloadPlugin) Register(r *plugin.Registry) {
	if p == nil || !p.opts.ContextOffload.Enabled {
		return
	}
	r.AfterToolMessages(p.afterToolMessages)
	r.BeforeModel(p.beforeModel)
}

func (p *contextOffloadPlugin) afterToolMessages(
	ctx context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Invocation == nil ||
		args.Invocation.Session == nil {
		return nil, nil
	}
	if err := validateSessionScope(args.Invocation.Session); err != nil {
		return nil, nil
	}
	sessionID := p.sessionID(args.Invocation.Session)
	if sessionID == "" {
		log.WarnfContext(ctx, "tencentdb context offload: session ID is empty")
		return nil, nil
	}
	pairs := newOffloadToolPairs(args.ToolCalls, args.ToolResultMessages)
	if len(pairs) == 0 {
		return nil, nil
	}
	client, err := p.contextOffloadClient()
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: gateway client unavailable: %v", err)
		return nil, nil
	}
	prompt, recent := offloadPromptContext(args.Messages)
	if _, err := client.ingest(ctx, offloadIngestRequest{
		SessionID:      sessionID,
		ToolPairs:      pairs,
		Prompt:         prompt,
		RecentMessages: recent,
	}); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: ingest failed: %v", err)
	}
	return nil, nil
}

func (p *contextOffloadPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if p == nil || args == nil || args.Request == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, nil
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, nil
	}
	sessionID := p.sessionID(inv.Session)
	if sessionID == "" {
		log.WarnfContext(ctx, "tencentdb context offload: session ID is empty")
		return nil, nil
	}
	client, err := p.contextOffloadClient()
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: gateway client unavailable: %v", err)
		return nil, nil
	}

	prompt, recent := offloadPromptContext(args.Request.Messages)
	p.ingestPrompt(ctx, client, sessionID, prompt, recent)

	if len(args.Request.Messages) == 0 {
		return nil, nil
	}
	totalTokens, messageTokens := p.countTokens(ctx, args.Request.Messages)
	contextWindow := offloadContextWindow(inv)
	ratio := float64(totalTokens) / float64(contextWindow)
	if ratio < p.opts.ContextOffload.CompactionRatio {
		return nil, nil
	}

	messages := make([]offloadMessage, 0, len(args.Request.Messages))
	for _, message := range args.Request.Messages {
		messages = append(messages, newOffloadMessage(message))
	}
	rsp, err := client.compact(ctx, offloadCompactRequest{
		SessionID:     sessionID,
		Messages:      messages,
		Ratio:         math.Min(ratio, 2),
		ContextWindow: contextWindow,
		TotalTokens:   totalTokens,
		MessageTokens: messageTokens,
	})
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: compact failed: %v", err)
		return nil, nil
	}
	if rsp == nil || len(rsp.Messages) == 0 {
		return nil, nil
	}
	compacted := make([]model.Message, 0, len(rsp.Messages))
	for _, message := range rsp.Messages {
		if !message.Role.IsValid() {
			log.WarnfContext(
				ctx,
				"tencentdb context offload: compact returned invalid role %q",
				message.Role,
			)
			return nil, nil
		}
		compacted = append(compacted, message.modelMessage())
	}
	if hasOrphanToolResults(compacted) {
		log.WarnfContext(ctx, "tencentdb context offload: compact returned orphan tool results")
		return nil, nil
	}
	args.Request.Messages = compacted
	return nil, nil
}

func (p *contextOffloadPlugin) ingestPrompt(
	ctx context.Context,
	client *offloadGatewayClient,
	sessionID string,
	prompt string,
	recent []offloadRecentMessage,
) {
	if prompt == "" || isInternalOffloadPrompt(prompt) ||
		!p.reservePrompt(sessionID, prompt) {
		return
	}
	if _, err := client.ingest(ctx, offloadIngestRequest{
		SessionID:      sessionID,
		ToolPairs:      []offloadToolPair{},
		Prompt:         prompt,
		RecentMessages: recent,
	}); err != nil {
		p.finishPrompt(sessionID, prompt, false)
		log.WarnfContext(ctx, "tencentdb context offload: prompt ingest failed: %v", err)
		return
	}
	p.finishPrompt(sessionID, prompt, true)
}

func (p *contextOffloadPlugin) reservePrompt(sessionID, prompt string) bool {
	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	if p.lastPrompt == nil {
		p.lastPrompt = make(map[string]string)
	}
	if p.lastPrompt[sessionID] == prompt {
		return false
	}
	if p.promptsInFlight == nil {
		p.promptsInFlight = make(map[offloadPromptKey]struct{})
	}
	key := offloadPromptKey{sessionID: sessionID, prompt: prompt}
	if _, ok := p.promptsInFlight[key]; ok {
		return false
	}
	p.promptsInFlight[key] = struct{}{}
	return true
}

func (p *contextOffloadPlugin) finishPrompt(
	sessionID string,
	prompt string,
	succeeded bool,
) {
	p.promptMu.Lock()
	defer p.promptMu.Unlock()
	delete(p.promptsInFlight, offloadPromptKey{
		sessionID: sessionID,
		prompt:    prompt,
	})
	if !succeeded {
		return
	}
	if _, ok := p.lastPrompt[sessionID]; !ok {
		if len(p.promptOrder) >= maxOffloadPromptSessions {
			delete(p.lastPrompt, p.promptOrder[0])
			p.promptOrder = p.promptOrder[1:]
		}
		p.promptOrder = append(p.promptOrder, sessionID)
	}
	p.lastPrompt[sessionID] = prompt
}

func (p *contextOffloadPlugin) countTokens(
	ctx context.Context,
	messages []model.Message,
) (int, []int) {
	counter := p.opts.ContextOffload.TokenCounter
	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}
	total, perMessage, err := countOffloadTokens(ctx, counter, messages)
	if err == nil {
		return total, perMessage
	}
	log.WarnfContext(ctx, "tencentdb context offload: token counter failed, using simple estimate: %v", err)
	total, perMessage, _ = countOffloadTokens(
		ctx,
		model.NewSimpleTokenCounter(),
		messages,
	)
	return total, perMessage
}

func countOffloadTokens(
	ctx context.Context,
	counter model.TokenCounter,
	messages []model.Message,
) (int, []int, error) {
	perMessage := make([]int, 0, len(messages))
	total := 0
	for _, message := range messages {
		tokens, err := counter.CountTokens(ctx, message)
		if err != nil {
			return 0, nil, err
		}
		if tokens < 0 {
			return 0, nil, fmt.Errorf("token counter returned negative count %d", tokens)
		}
		perMessage = append(perMessage, tokens)
		total += tokens
	}
	return total, perMessage, nil
}

func (p *contextOffloadPlugin) contextOffloadClient() (*offloadGatewayClient, error) {
	if p == nil {
		return nil, nil
	}
	if p.client != nil {
		return p.client, nil
	}
	return newOffloadGatewayClient(p.opts)
}

func (p *contextOffloadPlugin) sessionID(sess *session.Session) string {
	if p == nil {
		return ""
	}
	if p.opts.SessionKeyFunc != nil {
		return strings.TrimSpace(p.opts.SessionKeyFunc(sess))
	}
	return strings.TrimSpace(defaultSessionKey(sess))
}

func newOffloadToolPairs(
	calls []model.ToolCall,
	results []model.Message,
) []offloadToolPair {
	callsByID := make(map[string]model.ToolCall, len(calls))
	for _, call := range calls {
		if call.ID != "" {
			callsByID[call.ID] = call
		}
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	pairs := make([]offloadToolPair, 0, len(results))
	for _, result := range results {
		callID := strings.TrimSpace(result.ToolID)
		if callID == "" {
			continue
		}
		call := callsByID[callID]
		toolName := strings.TrimSpace(call.Function.Name)
		if toolName == "" {
			toolName = strings.TrimSpace(result.ToolName)
		}
		pairs = append(pairs, offloadToolPair{
			ToolName:   toolName,
			ToolCallID: callID,
			Params:     offloadToolParams(call.Function.Arguments),
			Result:     offloadToolResult(result),
			Timestamp:  timestamp,
		})
	}
	return pairs
}

func offloadToolParams(arguments []byte) any {
	if len(arguments) == 0 {
		return map[string]any{}
	}
	var params any
	if err := json.Unmarshal(arguments, &params); err == nil {
		return params
	}
	return string(arguments)
}

func offloadToolResult(message model.Message) any {
	if message.Content != "" {
		return message.Content
	}
	if len(message.ContentParts) > 0 {
		return message.ContentParts
	}
	return ""
}

func offloadPromptContext(
	messages []model.Message,
) (string, []offloadRecentMessage) {
	var prompt string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			prompt = strings.TrimSpace(messageText(messages[i]))
			if prompt != "" {
				break
			}
		}
	}
	if prompt == "" {
		return "", nil
	}
	prompt = truncateRunes(prompt, maxOffloadPromptRunes)
	normalizedPrompt := strings.ToLower(
		truncateRunes(strings.TrimSpace(prompt), 200),
	)
	recent := make([]offloadRecentMessage, 0, maxOffloadRecentMessages)
	for _, message := range messages {
		recentMessage, ok := newOffloadRecentMessage(
			message,
			normalizedPrompt,
		)
		if ok {
			recent = append(recent, recentMessage)
		}
	}
	if len(recent) > maxOffloadRecentMessages {
		recent = recent[len(recent)-maxOffloadRecentMessages:]
	}
	return prompt, recent
}

func newOffloadRecentMessage(
	message model.Message,
	normalizedPrompt string,
) (offloadRecentMessage, bool) {
	if message.Role != model.RoleUser && message.Role != model.RoleAssistant {
		return offloadRecentMessage{}, false
	}
	if message.Role == model.RoleAssistant && len(message.ToolCalls) > 0 {
		return offloadRecentMessage{}, false
	}
	content := strings.TrimSpace(messageText(message))
	if content == "" || isInternalHeartbeatMessage(content) {
		return offloadRecentMessage{}, false
	}
	if message.Role == model.RoleUser && utf8.RuneCountInString(content) <= 5 {
		return offloadRecentMessage{}, false
	}
	if message.Role == model.RoleAssistant &&
		utf8.RuneCountInString(content) <= 10 {
		return offloadRecentMessage{}, false
	}
	normalized := strings.ToLower(truncateRunes(content, 200))
	if normalizedPrompt != "" &&
		(normalized == normalizedPrompt ||
			strings.HasPrefix(normalized, normalizedPrompt) ||
			strings.HasPrefix(normalizedPrompt, normalized)) {
		return offloadRecentMessage{}, false
	}
	return offloadRecentMessage{
		Role:    message.Role,
		Content: truncateRunes(content, maxOffloadRecentRunes),
	}, true
}

func isInternalOffloadPrompt(prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	return strings.HasPrefix(prompt, "Pre-compaction") ||
		strings.HasPrefix(prompt, "[Inter-session message]") ||
		isInternalHeartbeatMessage(prompt)
}

func isInternalHeartbeatMessage(message string) bool {
	message = strings.TrimSpace(message)
	return strings.HasPrefix(message, heartbeatPromptPrefix) ||
		message == heartbeatReply
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func offloadContextWindow(inv *agent.Invocation) int {
	if inv == nil {
		return defaultModelContextWindow
	}
	if window, ok := agent.ModelContextWindowFromRunOptions(&inv.RunOptions); ok {
		return window
	}
	if inv.Model == nil {
		return defaultModelContextWindow
	}
	info := inv.Model.Info()
	if info.ContextWindow > 0 {
		return info.ContextWindow
	}
	if window, ok := model.LookupModelContextWindow(info.Name); ok {
		return window
	}
	return defaultModelContextWindow
}

func hasOrphanToolResults(messages []model.Message) bool {
	pending := make(map[string]int)
	for _, msg := range messages {
		if msg.Role == model.RoleAssistant {
			for _, call := range msg.ToolCalls {
				if call.ID != "" {
					pending[call.ID]++
				}
			}
			continue
		}
		if msg.Role != model.RoleTool || msg.ToolID == "" {
			continue
		}
		if pending[msg.ToolID] == 0 {
			return true
		}
		pending[msg.ToolID]--
	}
	return false
}
