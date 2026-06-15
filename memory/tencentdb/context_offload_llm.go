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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var errOffloadModelUnavailable = errors.New("context offload model unavailable")

type offloadModelClient interface {
	L1Summarize(context.Context, offloadL1Request) ([]offloadIndexEntry, error)
	L15Judge(context.Context, offloadL15Request) (offloadTaskJudgment, error)
	L2Generate(context.Context, offloadL2Request) (offloadL2Response, error)
}

type offloadL1Request struct {
	RecentMessages string            `json:"recentMessages"`
	ToolPairs      []offloadToolPair `json:"toolPairs"`
}

type offloadMMDContent struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	Path     string `json:"path"`
}

type offloadL15Request struct {
	RecentMessages    string             `json:"recentMessages"`
	CurrentMMD        *offloadMMDContent `json:"currentMmd,omitempty"`
	AvailableMMDMetas []offloadMMDMeta   `json:"availableMmdMetas"`
}

type offloadTaskJudgment struct {
	TaskCompleted       bool   `json:"taskCompleted"`
	IsContinuation      bool   `json:"isContinuation"`
	ContinuationMMDFile string `json:"continuationMmdFile"`
	NewTaskLabel        string `json:"newTaskLabel"`
	IsLongTask          bool   `json:"isLongTask"`
}

type offloadL2Request struct {
	ExistingMMD   string              `json:"existingMmd"`
	NewEntries    []offloadIndexEntry `json:"newEntries"`
	RecentHistory string              `json:"recentHistory"`
	CurrentTurn   string              `json:"currentTurn"`
	TaskLabel     string              `json:"taskLabel"`
	MMDPrefix     string              `json:"mmdPrefix"`
	MMDCharCount  int                 `json:"mmdCharCount"`
}

type offloadL2Response struct {
	FileAction    string                  `json:"file_action"`
	MMDContent    string                  `json:"mmd_content"`
	ReplaceBlocks []offloadL2ReplaceBlock `json:"replace_blocks"`
	NodeMapping   map[string]string       `json:"node_mapping"`
}

func (r *offloadL2Response) UnmarshalJSON(data []byte) error {
	type alias offloadL2Response
	var raw struct {
		alias
		FileActionCamel    string                  `json:"fileAction"`
		MMDContentCamel    string                  `json:"mmdContent"`
		ReplaceBlocksCamel []offloadL2ReplaceBlock `json:"replaceBlocks"`
		NodeMappingCamel   map[string]string       `json:"nodeMapping"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = offloadL2Response(raw.alias)
	if r.FileAction == "" {
		r.FileAction = raw.FileActionCamel
	}
	if r.MMDContent == "" {
		r.MMDContent = raw.MMDContentCamel
	}
	if len(r.ReplaceBlocks) == 0 {
		r.ReplaceBlocks = raw.ReplaceBlocksCamel
	}
	if len(r.NodeMapping) == 0 {
		r.NodeMapping = raw.NodeMappingCamel
	}
	return nil
}

type offloadL2ReplaceBlock struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

func (b *offloadL2ReplaceBlock) UnmarshalJSON(data []byte) error {
	type alias offloadL2ReplaceBlock
	var raw struct {
		alias
		StartLineCamel int `json:"startLine"`
		EndLineCamel   int `json:"endLine"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = offloadL2ReplaceBlock(raw.alias)
	if b.StartLine == 0 {
		b.StartLine = raw.StartLineCamel
	}
	if b.EndLine == 0 {
		b.EndLine = raw.EndLineCamel
	}
	return nil
}

type localOffloadModelClient struct {
	model model.Model
	opts  Options
}

type backendOffloadModelClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func (p *contextOffloadPlugin) newOffloadModelClient() offloadModelClient {
	cfg := p.opts.ContextOffload
	switch strings.TrimSpace(cfg.Mode) {
	case ContextOffloadModeBackend:
		if strings.TrimSpace(cfg.Backend.URL) == "" {
			return nil
		}
		return &backendOffloadModelClient{
			baseURL: strings.TrimRight(strings.TrimSpace(cfg.Backend.URL), "/"),
			apiKey:  cfg.Backend.APIKey,
			httpClient: &http.Client{
				Timeout: 2 * time.Minute,
			},
		}
	case ContextOffloadModeCollect:
		return nil
	default:
		return &localOffloadModelClient{model: cfg.Model, opts: p.opts}
	}
}

func (c *localOffloadModelClient) L1Summarize(
	ctx context.Context,
	req offloadL1Request,
) ([]offloadIndexEntry, error) {
	if c == nil || c.model == nil {
		return nil, errOffloadModelUnavailable
	}
	raw, err := c.generateJSON(ctx, l1SystemPrompt, buildL1UserPrompt(req))
	if err != nil {
		return nil, err
	}
	return parseL1Entries(raw)
}

func (c *localOffloadModelClient) L15Judge(
	ctx context.Context,
	req offloadL15Request,
) (offloadTaskJudgment, error) {
	if c == nil || c.model == nil {
		return offloadTaskJudgment{}, errOffloadModelUnavailable
	}
	raw, err := c.generateJSON(ctx, l15SystemPrompt, buildL15UserPrompt(req))
	if err != nil {
		return offloadTaskJudgment{}, err
	}
	var parsed offloadTaskJudgment
	if err := unmarshalExtractedJSON(raw, &parsed); err != nil {
		return offloadTaskJudgment{}, err
	}
	return parsed, nil
}

func (c *localOffloadModelClient) L2Generate(
	ctx context.Context,
	req offloadL2Request,
) (offloadL2Response, error) {
	if c == nil || c.model == nil {
		return offloadL2Response{}, errOffloadModelUnavailable
	}
	raw, err := c.generateJSON(ctx, l2SystemPrompt, buildL2UserPrompt(req))
	if err != nil {
		return offloadL2Response{}, err
	}
	var parsed offloadL2Response
	if err := unmarshalExtractedJSON(raw, &parsed); err != nil {
		return offloadL2Response{}, err
	}
	return normalizeL2Response(parsed), nil
}

func (c *localOffloadModelClient) generateJSON(
	ctx context.Context,
	systemPrompt string,
	userPrompt string,
) (string, error) {
	temp := 0.2
	maxTokens := 2048
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(systemPrompt),
			model.NewUserMessage(userPrompt),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temp,
			MaxTokens:   &maxTokens,
		},
	}
	ch, err := c.model.GenerateContent(ctx, req)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for rsp := range ch {
		if rsp == nil {
			continue
		}
		if rsp.Error != nil {
			return "", fmt.Errorf("context offload model error: %s", rsp.Error.Message)
		}
		for _, choice := range rsp.Choices {
			if choice.Delta.Content != "" {
				b.WriteString(choice.Delta.Content)
				continue
			}
			if choice.Message.Content != "" {
				b.WriteString(choice.Message.Content)
			}
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", errors.New("context offload model returned empty response")
	}
	return b.String(), nil
}

func (c *backendOffloadModelClient) L1Summarize(
	ctx context.Context,
	req offloadL1Request,
) ([]offloadIndexEntry, error) {
	var rsp struct {
		Entries []offloadIndexEntry `json:"entries"`
	}
	if err := c.post(ctx, "/offload/v1/l1/summarize", req, &rsp); err != nil {
		return nil, err
	}
	return rsp.Entries, nil
}

func (c *backendOffloadModelClient) L15Judge(
	ctx context.Context,
	req offloadL15Request,
) (offloadTaskJudgment, error) {
	var rsp offloadTaskJudgment
	if err := c.post(ctx, "/offload/v1/l15/judge", req, &rsp); err != nil {
		return offloadTaskJudgment{}, err
	}
	return rsp, nil
}

func (c *backendOffloadModelClient) L2Generate(
	ctx context.Context,
	req offloadL2Request,
) (offloadL2Response, error) {
	var rsp offloadL2Response
	if err := c.post(ctx, "/offload/v1/l2/generate", req, &rsp); err != nil {
		return offloadL2Response{}, err
	}
	return normalizeL2Response(rsp), nil
}

func (c *backendOffloadModelClient) post(
	ctx context.Context,
	path string,
	req any,
	rsp any,
) error {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return errOffloadModelUnavailable
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	httpRsp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpRsp.Body.Close()
	limited := io.LimitReader(httpRsp.Body, defaultMaxBodyBytes)
	b, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if httpRsp.StatusCode < 200 || httpRsp.StatusCode >= 300 {
		return fmt.Errorf("context offload backend %s: %s", httpRsp.Status, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, rsp)
}

const l1SystemPrompt = `你是工具结果摘要器。把 tool call/result pairs 压缩为高密度 JSON 数组。
每个对象必须包含 tool_call、summary、tool_call_id、timestamp、score。
summary 不超过 200 个中文字符，说明工具结果对当前任务的推进、结论或阻塞。score 为 0-10，表示摘要替代原文的可靠程度。
只输出合法 JSON 数组，不要输出解释。`

func buildL1UserPrompt(req offloadL1Request) string {
	var b strings.Builder
	b.WriteString("## 最近对话\n")
	b.WriteString(req.RecentMessages)
	b.WriteString("\n\n## 工具结果\n")
	for i, pair := range req.ToolPairs {
		b.WriteString(fmt.Sprintf("### Pair %d\n", i+1))
		b.WriteString("tool_call_id: " + pair.ToolCallID + "\n")
		b.WriteString("timestamp: " + pair.Timestamp + "\n")
		b.WriteString("tool: " + pair.ToolName + "\n")
		b.WriteString("params: " + truncateRunes(pair.Params, 500) + "\n")
		b.WriteString("result_ref: " + pair.ResultRef + "\n")
		b.WriteString("result: " + truncateRunes(pair.Result, 2000) + "\n\n")
	}
	return b.String()
}

const l15SystemPrompt = `你是任务生命周期判断器。根据最近对话、当前 Mermaid 和历史 Mermaid metadata，判断当前是否是长任务、是否延续旧任务、是否需要新任务画布。
输出 JSON 对象：taskCompleted(boolean), isLongTask(boolean), isContinuation(boolean), continuationMmdFile(string|null), newTaskLabel(string|null)。
普通问答或闲聊 isLongTask=false；多步工程、排查、实现、测试类任务 isLongTask=true。只输出 JSON。`

func buildL15UserPrompt(req offloadL15Request) string {
	var b strings.Builder
	b.WriteString("## 最近对话\n")
	b.WriteString(req.RecentMessages)
	b.WriteString("\n\n## 当前 MMD\n")
	if req.CurrentMMD == nil {
		b.WriteString("(none)\n")
	} else {
		b.WriteString("file: " + req.CurrentMMD.Filename + "\n")
		b.WriteString(req.CurrentMMD.Content)
		b.WriteString("\n")
	}
	b.WriteString("\n## 历史 MMD metadata\n")
	if len(req.AvailableMMDMetas) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, meta := range req.AvailableMMDMetas {
			b.WriteString(fmt.Sprintf("- %s goal=%s updated=%s done=%d doing=%d todo=%d\n",
				meta.Filename, meta.TaskGoal, meta.UpdatedTime,
				meta.DoneCount, meta.DoingCount, meta.TodoCount))
		}
	}
	return b.String()
}

const l2SystemPrompt = `你是 AI 任务拓扑架构师。把工具摘要 entries 更新为紧凑 Mermaid flowchart TD。
输出 JSON 对象：file_action("write"或"replace"), mmd_content, replace_blocks, node_mapping。
每个新 tool_call_id 必须出现在 node_mapping。节点 ID 使用给定前缀，如 001-N1。MMD 需要包含 metadata 行和 flowchart TD。只输出 JSON。`

func buildL2UserPrompt(req offloadL2Request) string {
	var b strings.Builder
	b.WriteString("## 近期历史\n")
	b.WriteString(req.RecentHistory)
	b.WriteString("\n\n## 当前轮\n")
	b.WriteString(req.CurrentTurn)
	b.WriteString("\n\n## MMD prefix\n")
	b.WriteString(req.MMDPrefix)
	b.WriteString("\n\n## task label\n")
	b.WriteString(req.TaskLabel)
	b.WriteString("\n\n## existing MMD\n")
	if strings.TrimSpace(req.ExistingMMD) == "" {
		b.WriteString("(empty)\n")
	} else {
		lines := strings.Split(req.ExistingMMD, "\n")
		for i, line := range lines {
			b.WriteString(fmt.Sprintf("L%d: %s\n", i+1, line))
		}
	}
	b.WriteString("\n## new entries\n")
	for i, entry := range req.NewEntries {
		b.WriteString(fmt.Sprintf("%d. [%s] %s -> %s (%s)\n",
			i+1, entry.ToolCallID, entry.ToolCall, entry.Summary, entry.Timestamp))
	}
	return b.String()
}

func normalizeL1Entries(
	entries []offloadIndexEntry,
	pairs []offloadToolPair,
) []offloadIndexEntry {
	byID := make(map[string]offloadToolPair, len(pairs))
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		byID[pair.ToolCallID] = pair
	}
	out := make([]offloadIndexEntry, 0, len(pairs))
	for _, entry := range entries {
		pair, ok := byID[entry.ToolCallID]
		if !ok {
			continue
		}
		if strings.TrimSpace(entry.ToolCall) == "" {
			entry.ToolCall = pair.ToolName
		}
		if strings.TrimSpace(entry.Summary) == "" {
			entry.Summary = summarizeToolResult(pair.Result)
		}
		if strings.TrimSpace(entry.Timestamp) == "" {
			entry.Timestamp = pair.Timestamp
		}
		entry.ResultRef = pair.ResultRef
		entry.NodeID = nil
		if entry.Score <= 0 {
			entry.Score = 5
		}
		out = append(out, entry)
		seen[entry.ToolCallID] = struct{}{}
	}
	for _, pair := range pairs {
		if _, ok := seen[pair.ToolCallID]; ok {
			continue
		}
		out = append(out, fallbackL1Entry(pair))
	}
	return out
}

func parseL1Entries(raw string) ([]offloadIndexEntry, error) {
	raw = extractJSON(raw)
	if raw == "" {
		return nil, errors.New("no JSON array found")
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	entries := make([]offloadIndexEntry, 0, len(items))
	for _, item := range items {
		toolCallID := stringField(item, "tool_call_id")
		if toolCallID == "" {
			toolCallID = stringField(item, "toolCallId")
		}
		if toolCallID == "" {
			continue
		}
		entries = append(entries, offloadIndexEntry{
			ToolCallID: toolCallID,
			ToolCall:   flexibleStringField(item, "tool_call", "toolCall"),
			Summary:    flexibleStringField(item, "summary"),
			Timestamp:  stringField(item, "timestamp"),
			Score:      numberField(item, "score"),
		})
	}
	return entries, nil
}

func stringField(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := item[key].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func flexibleStringField(item map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := item[key]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
		return fmt.Sprint(v)
	}
	return ""
}

func numberField(item map[string]any, key string) float64 {
	switch v := item[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func fallbackL1Entry(pair offloadToolPair) offloadIndexEntry {
	return offloadIndexEntry{
		Timestamp:  pair.Timestamp,
		NodeID:     nil,
		ToolCall:   pair.ToolName + compactParams(pair.Params),
		Summary:    summarizeToolResult(pair.Result),
		ResultRef:  pair.ResultRef,
		ToolCallID: pair.ToolCallID,
		Score:      4,
	}
}

func compactParams(params string) string {
	params = strings.TrimSpace(params)
	if params == "" {
		return ""
	}
	return "(" + truncateRunes(strings.Join(strings.Fields(params), " "), 160) + ")"
}

func summarizeToolResult(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "Tool returned an empty result."
	}
	normalized := strings.Join(strings.Fields(content), " ")
	return truncateRunes(normalized, 600)
}

func fallbackTaskJudgment(messages []model.Message) offloadTaskJudgment {
	latest := strings.ToLower(latestUserMessageText(messages))
	if latest == "" {
		return offloadTaskJudgment{TaskCompleted: true}
	}
	shortSignals := []string{"what is", "是什么", "解释", "why", "为什么"}
	longSignals := []string{"实现", "修复", "排查", "测试", "改", "新增", "refactor", "implement", "fix", "debug", "test"}
	for _, signal := range longSignals {
		if strings.Contains(latest, signal) {
			return offloadTaskJudgment{IsLongTask: true, NewTaskLabel: labelFromText(latest)}
		}
	}
	for _, signal := range shortSignals {
		if strings.Contains(latest, signal) {
			return offloadTaskJudgment{TaskCompleted: true, IsLongTask: false}
		}
	}
	return offloadTaskJudgment{IsLongTask: true, NewTaskLabel: labelFromText(latest)}
}

func normalizeTaskJudgment(got, fallback offloadTaskJudgment) offloadTaskJudgment {
	if !got.IsLongTask && !got.TaskCompleted && !got.IsContinuation &&
		strings.TrimSpace(got.NewTaskLabel) == "" &&
		strings.TrimSpace(got.ContinuationMMDFile) == "" {
		return fallback
	}
	if got.IsLongTask && strings.TrimSpace(got.NewTaskLabel) == "" &&
		strings.TrimSpace(got.ContinuationMMDFile) == "" {
		got.NewTaskLabel = fallback.NewTaskLabel
	}
	return got
}

func fallbackL2Response(req offloadL2Request) offloadL2Response {
	now := time.Now().Format(time.RFC3339Nano)
	existing := strings.TrimSpace(stripMermaidFence(req.ExistingMMD))
	taskGoal := strings.TrimSpace(req.TaskLabel)
	if taskGoal == "" {
		taskGoal = "task"
	}
	mapping := make(map[string]string, len(req.NewEntries))
	var b strings.Builder
	if existing == "" {
		b.WriteString(fmt.Sprintf(
			"%%{ \"taskGoal\": %q, \"progress\": \"60\", \"createdTime\": %q, \"updatedTime\": %q }%%\n",
			taskGoal,
			now,
			now,
		))
		b.WriteString("flowchart TD\n")
	} else {
		b.WriteString(existing)
		if !strings.HasSuffix(existing, "\n") {
			b.WriteString("\n")
		}
	}
	startOrdinal := 1
	if existing != "" {
		startOrdinal = nextFallbackNodeOrdinal(existing, req.MMDPrefix)
	}
	for i, entry := range req.NewEntries {
		ordinal := startOrdinal + i
		nodeID := fmt.Sprintf("%s-N%d", req.MMDPrefix, ordinal)
		mapping[entry.ToolCallID] = nodeID
		b.WriteString(fmt.Sprintf(
			"  %s[\"%s<br/>status: done<br/>summary: %s<br/>ref: %s<br/>Timestamp: %s\"]\n",
			mermaidNodeID(nodeID),
			escapeMermaidLabel(truncateRunes(entry.ToolCall, 80)),
			escapeMermaidLabel(truncateRunes(entry.Summary, 120)),
			escapeMermaidLabel(entry.ResultRef),
			escapeMermaidLabel(entry.Timestamp),
		))
		if ordinal > 1 {
			prev := fmt.Sprintf("%s-N%d", req.MMDPrefix, ordinal-1)
			b.WriteString(fmt.Sprintf("  %s --> %s\n", mermaidNodeID(prev), mermaidNodeID(nodeID)))
		}
	}
	return offloadL2Response{
		FileAction:  "write",
		MMDContent:  b.String(),
		NodeMapping: mapping,
	}
}

func nextFallbackNodeOrdinal(existing string, prefix string) int {
	base := mermaidNodeID(fmt.Sprintf("%s-N", prefix))
	re := regexp.MustCompile(regexp.QuoteMeta(base) + `(\d+)`)
	maxOrdinal := 0
	for _, match := range re.FindAllStringSubmatch(existing, -1) {
		if len(match) < 2 {
			continue
		}
		ordinal, err := strconv.Atoi(match[1])
		if err == nil && ordinal > maxOrdinal {
			maxOrdinal = ordinal
		}
	}
	return maxOrdinal + 1
}

func normalizeL2Response(rsp offloadL2Response) offloadL2Response {
	if rsp.FileAction == "" {
		rsp.FileAction = "write"
	}
	if rsp.NodeMapping == nil {
		rsp.NodeMapping = map[string]string{}
	}
	return rsp
}

func unmarshalExtractedJSON(raw string, target any) error {
	raw = extractJSON(raw)
	if raw == "" {
		return errors.New("no JSON object found")
	}
	return json.Unmarshal([]byte(raw), target)
}

func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	startObj := strings.Index(raw, "{")
	startArr := strings.Index(raw, "[")
	start := -1
	endChar := byte('}')
	if startObj >= 0 && (startArr < 0 || startObj < startArr) {
		start = startObj
		endChar = '}'
	} else if startArr >= 0 {
		start = startArr
		endChar = ']'
	}
	if start < 0 {
		return ""
	}
	end := strings.LastIndexByte(raw, endChar)
	if end < start {
		return ""
	}
	return raw[start : end+1]
}

func recentMessagesText(messages []model.Message, limit int) string {
	if limit <= 0 {
		limit = 6
	}
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for _, msg := range messages[start:] {
		text := offloadMessageText(msg)
		if text == "" {
			continue
		}
		b.WriteString("[")
		b.WriteString(string(msg.Role))
		b.WriteString("] ")
		b.WriteString(truncateRunes(text, 500))
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func latestUserMessageText(messages []model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			return offloadMessageText(messages[i])
		}
	}
	return ""
}

func offloadMessageText(msg model.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return strings.TrimSpace(msg.Content)
	}
	var parts []string
	for _, part := range msg.ContentParts {
		if part.Text != nil {
			parts = append(parts, *part.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func offloadToolResultText(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	return offloadMessageText(msg)
}

func labelFromText(text string) string {
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return "task"
	}
	if len(words) > 5 {
		words = words[:5]
	}
	return safeTaskLabel(strings.Join(words, "-"))
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func mermaidNodeID(nodeID string) string {
	nodeID = unsafeFilenameChars.ReplaceAllString(nodeID, "_")
	nodeID = strings.Trim(nodeID, "_")
	if nodeID == "" {
		return "node"
	}
	if nodeID[0] >= '0' && nodeID[0] <= '9' {
		return "n_" + nodeID
	}
	return nodeID
}

func escapeMermaidLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
