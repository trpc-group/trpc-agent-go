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
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	offloadBoundaryLong    = "long"
	offloadBoundaryShort   = "short"
	offloadBoundaryPending = "pending"
	offloadWaitNodeID      = "wait"
	offloadDirPerm         = 0o700
	offloadFilePerm        = 0o600
)

type offloadStorageContext struct {
	DataRoot     string
	DataDir      string
	RefsDir      string
	MMDsDir      string
	OffloadJSONL string
	StateFile    string
	Registry     string
	AgentName    string
	SessionID    string
	SessionKey   string
}

type offloadIndexEntry struct {
	Timestamp  string   `json:"timestamp"`
	NodeID     *string  `json:"node_id"`
	ToolCall   string   `json:"tool_call"`
	Summary    string   `json:"summary"`
	ResultRef  string   `json:"result_ref"`
	ToolCallID string   `json:"tool_call_id"`
	SessionKey string   `json:"session_key,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Offloaded  any      `json:"offloaded,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
}

type offloadToolPair struct {
	ToolName   string
	ToolCallID string
	Params     string
	Result     string
	ResultRef  string
	Timestamp  string
}

type offloadBoundary struct {
	StartIndex int    `json:"start_index"`
	Result     string `json:"result"`
	TargetMMD  string `json:"target_mmd,omitempty"`
}

type offloadState struct {
	ActiveMMDFile           string            `json:"active_mmd_file,omitempty"`
	ActiveMMDID             string            `json:"active_mmd_id,omitempty"`
	MMDCounter              int               `json:"mmd_counter,omitempty"`
	LastSessionKey          string            `json:"last_session_key,omitempty"`
	LastOffloadedToolCallID string            `json:"last_offloaded_tool_call_id,omitempty"`
	LastL15JudgedToolCallID string            `json:"last_l15_judged_tool_call_id,omitempty"`
	LastL2TriggerTime       string            `json:"last_l2_trigger_time,omitempty"`
	L15Settled              bool              `json:"l15_settled,omitempty"`
	Boundaries              []offloadBoundary `json:"boundaries,omitempty"`
	ConfirmedOffloadIDs     []string          `json:"confirmed_offload_ids,omitempty"`
	DeletedOffloadIDs       []string          `json:"deleted_offload_ids,omitempty"`
	LastKnownTotalTokens    int               `json:"last_known_total_tokens,omitempty"`
	LastKnownMessageCount   int               `json:"last_known_message_count,omitempty"`
}

type offloadMMDMeta struct {
	Filename      string `json:"filename"`
	Path          string `json:"path"`
	TaskGoal      string `json:"taskGoal"`
	DoneCount     int    `json:"doneCount"`
	DoingCount    int    `json:"doingCount"`
	TodoCount     int    `json:"todoCount"`
	UpdatedTime   string `json:"updatedTime,omitempty"`
	NodeSummaries []struct {
		NodeID  string `json:"nodeId"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
	} `json:"nodeSummaries,omitempty"`
}

func newOffloadState() *offloadState {
	return &offloadState{}
}

func (s *offloadState) addConfirmed(id string) {
	s.ConfirmedOffloadIDs = addUniqueString(s.ConfirmedOffloadIDs, id)
}

func (s *offloadState) addDeleted(id string) {
	s.DeletedOffloadIDs = addUniqueString(s.DeletedOffloadIDs, id)
}

func (s *offloadState) confirmedSet() map[string]struct{} {
	return stringSet(s.ConfirmedOffloadIDs)
}

func (s *offloadState) deletedSet() map[string]struct{} {
	return stringSet(s.DeletedOffloadIDs)
}

func addUniqueString(values []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return values
	}
	for _, existing := range values {
		if existing == v {
			return values
		}
	}
	return append(values, v)
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

func newOffloadStorageContext(
	opts Options,
	sess *session.Session,
	agentName string,
) offloadStorageContext {
	root := strings.TrimSpace(opts.ContextOffload.DataDir)
	if root == "" {
		root = defaultContextOffloadDataDir
	}
	sessionKey := defaultSessionKeyWithFunc(opts, sess)
	sessionID := "session"
	if sess != nil && strings.TrimSpace(sess.ID) != "" {
		sessionID = safeFilename(sess.ID)
	} else if sessionKey != "" {
		sessionID = safeFilename(base64.RawURLEncoding.EncodeToString([]byte(sessionKey)))
	}
	scope := safeScopeName(sess, agentName)
	dataDir := filepath.Join(root, scope, sessionID)
	return offloadStorageContext{
		DataRoot:     root,
		DataDir:      dataDir,
		RefsDir:      filepath.Join(dataDir, "refs"),
		MMDsDir:      filepath.Join(dataDir, "mmds"),
		OffloadJSONL: filepath.Join(dataDir, "offload-"+sessionID+".jsonl"),
		StateFile:    filepath.Join(dataDir, "state.json"),
		Registry:     filepath.Join(dataDir, "sessions-registry.json"),
		AgentName:    scope,
		SessionID:    sessionID,
		SessionKey:   sessionKey,
	}
}

func defaultSessionKeyWithFunc(opts Options, sess *session.Session) string {
	if opts.SessionKeyFunc != nil {
		return opts.SessionKeyFunc(sess)
	}
	return defaultSessionKey(sess)
}

func safeScopeName(sess *session.Session, agentName string) string {
	parts := []string{"agent"}
	if sess != nil {
		parts = append(parts, sess.AppName, sess.UserID)
	}
	parts = append(parts, agentName)
	raw := strings.Join(parts, "_")
	return safeFilename(raw)
}

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func safeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "session"
	}
	s = unsafeFilenameChars.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "session"
	}
	return s
}

func ensureOffloadDirs(ctx offloadStorageContext) error {
	if err := os.MkdirAll(ctx.DataRoot, offloadDirPerm); err != nil {
		return err
	}
	if err := os.MkdirAll(ctx.DataDir, offloadDirPerm); err != nil {
		return err
	}
	if err := os.MkdirAll(ctx.RefsDir, offloadDirPerm); err != nil {
		return err
	}
	return os.MkdirAll(ctx.MMDsDir, offloadDirPerm)
}

func registerOffloadSession(ctx offloadStorageContext) {
	if ctx.SessionKey == "" || ctx.SessionID == "" {
		return
	}
	registry := map[string]map[string]string{}
	if b, err := os.ReadFile(ctx.Registry); err == nil {
		_ = json.Unmarshal(b, &registry)
	}
	registry[ctx.SessionKey] = map[string]string{
		"session_id":   ctx.SessionID,
		"offload_file": filepath.Base(ctx.OffloadJSONL),
		"updated_at":   time.Now().Format(time.RFC3339Nano),
	}
	b, err := json.MarshalIndent(registry, "", "  ")
	if err == nil {
		_ = os.WriteFile(ctx.Registry, b, offloadFilePerm)
	}
}

func readOffloadState(ctx offloadStorageContext) (*offloadState, error) {
	b, err := os.ReadFile(ctx.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return newOffloadState(), nil
	}
	if err != nil {
		return nil, err
	}
	var state offloadState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func writeOffloadState(ctx offloadStorageContext, state *offloadState) error {
	if state == nil {
		return nil
	}
	if err := ensureOffloadDirs(ctx); err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ctx.StateFile, b, offloadFilePerm)
}

func writeOffloadRef(
	ctx offloadStorageContext,
	call model.ToolCall,
	msg model.Message,
) (string, error) {
	if msg.ToolID == "" {
		return "", errors.New("tool id is required")
	}
	if err := ensureOffloadDirs(ctx); err != nil {
		return "", err
	}
	now := time.Now()
	resultText := offloadToolResultText(msg)
	sum := sha256.Sum256([]byte(msg.ToolID + "\x00" + resultText))
	refID := fmt.Sprintf(
		"ref_%s_%s",
		now.Format("20060102_150405_000000000"),
		hex.EncodeToString(sum[:])[:12],
	)
	ref := filepath.ToSlash(filepath.Join("refs", refID+".md"))
	path := filepath.Join(ctx.DataDir, filepath.FromSlash(ref))
	entry := offloadIndexEntry{
		Timestamp:  now.Format(time.RFC3339Nano),
		ToolCall:   toolCallName(call, msg),
		ResultRef:  ref,
		ToolCallID: msg.ToolID,
		SessionKey: ctx.SessionKey,
	}
	return ref, os.WriteFile(path, []byte(renderOffloadRef(entry, call, resultText)), offloadFilePerm)
}

func renderOffloadRef(
	entry offloadIndexEntry,
	call model.ToolCall,
	content string,
) string {
	var b strings.Builder
	b.WriteString("# Tool Result\n\n")
	b.WriteString("- tool_call_id: " + entry.ToolCallID + "\n")
	b.WriteString("- tool_name: " + entry.ToolCall + "\n")
	b.WriteString("- timestamp: " + entry.Timestamp + "\n")
	if entry.NodeID != nil {
		b.WriteString("- node_id: " + *entry.NodeID + "\n")
	}
	b.WriteString("- result_ref: " + entry.ResultRef + "\n\n")
	if len(call.Function.Arguments) > 0 {
		b.WriteString("## Arguments\n\n```json\n")
		b.Write(call.Function.Arguments)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("## Result\n\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func appendOffloadEntries(ctx offloadStorageContext, entries []offloadIndexEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := ensureOffloadDirs(ctx); err != nil {
		return err
	}
	existing, err := readAllOffloadEntries(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		if entry.ToolCallID != "" {
			seen[entry.ToolCallID] = struct{}{}
		}
	}
	f, err := os.OpenFile(ctx.OffloadJSONL, os.O_CREATE|os.O_WRONLY|os.O_APPEND, offloadFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, entry := range entries {
		if entry.ToolCallID == "" {
			continue
		}
		if _, ok := seen[entry.ToolCallID]; ok {
			continue
		}
		entry.SessionKey = ctx.SessionKey
		b, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
		seen[entry.ToolCallID] = struct{}{}
	}
	return nil
}

func readAllOffloadEntries(ctx offloadStorageContext) ([]offloadIndexEntry, error) {
	f, err := os.Open(ctx.OffloadJSONL)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []offloadIndexEntry
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry offloadIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.ToolCallID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

func rewriteOffloadEntries(ctx offloadStorageContext, entries []offloadIndexEntry) error {
	if err := ensureOffloadDirs(ctx); err != nil {
		return err
	}
	var b strings.Builder
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(ctx.OffloadJSONL, []byte(b.String()), offloadFilePerm)
}

func readRecentOffloadEntries(
	ctx offloadStorageContext,
	maxEntries int,
) ([]offloadIndexEntry, error) {
	entries, err := readAllOffloadEntries(ctx)
	if err != nil {
		return nil, err
	}
	if maxEntries <= 0 {
		maxEntries = defaultContextOffloadMaxEntries
	}
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	return entries, nil
}

func readMMD(ctx offloadStorageContext, filename string) (string, error) {
	path, err := safeMMDPath(ctx, filename)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeMMD(ctx offloadStorageContext, filename, content string) error {
	path, err := safeMMDPath(ctx, filename)
	if err != nil {
		return err
	}
	if err := ensureOffloadDirs(ctx); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(stripMermaidFence(content))+"\n"), offloadFilePerm)
}

func safeMMDPath(ctx offloadStorageContext, filename string) (string, error) {
	cleaned := filepath.Base(safeFilename(filename))
	if cleaned == "" || cleaned == "." {
		return "", errors.New("invalid mmd filename")
	}
	if !strings.HasSuffix(cleaned, ".mmd") {
		cleaned += ".mmd"
	}
	path := filepath.Join(ctx.MMDsDir, cleaned)
	rel, err := filepath.Rel(filepath.Clean(ctx.MMDsDir), filepath.Clean(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", errors.New("invalid mmd filename")
	}
	return path, nil
}

func readActiveMMD(ctx offloadStorageContext, state *offloadState) (*offloadMMDContent, error) {
	if state == nil || state.ActiveMMDFile == "" {
		return nil, nil
	}
	content, err := readMMD(ctx, state.ActiveMMDFile)
	if err != nil {
		return nil, err
	}
	return &offloadMMDContent{
		Filename: state.ActiveMMDFile,
		Content:  content,
		Path:     filepath.ToSlash(filepath.Join("mmds", state.ActiveMMDFile)),
	}, nil
}

func listMMDMetas(ctx offloadStorageContext) ([]offloadMMDMeta, error) {
	entries, err := os.ReadDir(ctx.MMDsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var metas []offloadMMDMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mmd") {
			continue
		}
		content, err := readMMD(ctx, entry.Name())
		if err != nil {
			continue
		}
		metas = append(metas, parseMMDMeta(entry.Name(), content))
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedTime > metas[j].UpdatedTime
	})
	return metas, nil
}

func parseMMDMeta(filename, content string) offloadMMDMeta {
	meta := offloadMMDMeta{
		Filename: filename,
		Path:     filepath.ToSlash(filepath.Join("mmds", filename)),
		TaskGoal: strings.TrimSuffix(filename, ".mmd"),
	}
	if start := strings.Index(content, "%%{"); start >= 0 {
		if end := strings.Index(content[start:], "}%%"); end >= 0 {
			raw := strings.TrimSpace(content[start+2 : start+end+1])
			var parsed map[string]any
			if json.Unmarshal([]byte(raw), &parsed) == nil {
				if v, ok := parsed["taskGoal"].(string); ok {
					meta.TaskGoal = v
				}
				if v, ok := parsed["updatedTime"].(string); ok {
					meta.UpdatedTime = v
				}
			}
		}
	}
	meta.DoneCount = strings.Count(content, "status: done")
	meta.DoingCount = strings.Count(content, "status: doing")
	meta.TodoCount = strings.Count(content, "status: todo")
	return meta
}

func applyL2Response(
	ctx offloadStorageContext,
	filename string,
	rsp offloadL2Response,
) error {
	switch rsp.FileAction {
	case "replace":
		existing, err := readMMD(ctx, filename)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return writeMMD(ctx, filename, rsp.MMDContent)
			}
			return err
		}
		return writeMMD(ctx, filename, applyMMDReplaceBlocks(existing, rsp.ReplaceBlocks))
	case "write", "":
		return writeMMD(ctx, filename, rsp.MMDContent)
	default:
		return writeMMD(ctx, filename, rsp.MMDContent)
	}
}

func applyMMDReplaceBlocks(existing string, blocks []offloadL2ReplaceBlock) string {
	if len(blocks) == 0 {
		return existing
	}
	ordered := append([]offloadL2ReplaceBlock(nil), blocks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartLine == ordered[j].StartLine {
			return ordered[i].EndLine > ordered[j].EndLine
		}
		return ordered[i].StartLine > ordered[j].StartLine
	})
	lines := strings.Split(existing, "\n")
	for _, block := range ordered {
		start := block.StartLine - 1
		end := block.EndLine
		if start < 0 {
			start = 0
		}
		if end < start {
			end = start
		}
		if start > len(lines) {
			start = len(lines)
		}
		if end > len(lines) {
			end = len(lines)
		}
		replacement := strings.Split(strings.TrimSpace(stripMermaidFence(block.Content)), "\n")
		next := append([]string{}, lines[:start]...)
		next = append(next, replacement...)
		next = append(next, lines[end:]...)
		lines = next
	}
	return strings.Join(lines, "\n")
}

func backfillOffloadNodeIDs(
	ctx offloadStorageContext,
	mapping map[string]string,
	eligible []offloadIndexEntry,
) error {
	if len(mapping) == 0 && len(eligible) == 0 {
		return nil
	}
	entries, err := readAllOffloadEntries(ctx)
	if err != nil {
		return err
	}
	fallback := ""
	nodes := make([]string, 0, len(mapping))
	for _, node := range mapping {
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) > 0 {
		sort.Strings(nodes)
		fallback = nodes[0]
	}
	eligibleIDs := make(map[string]struct{}, len(eligible))
	for _, entry := range eligible {
		eligibleIDs[entry.ToolCallID] = struct{}{}
	}
	changed := false
	for i := range entries {
		if node, ok := mapping[entries[i].ToolCallID]; ok && node != "" {
			entries[i].NodeID = stringPtr(node)
			changed = true
			continue
		}
		if _, ok := eligibleIDs[entries[i].ToolCallID]; ok && entries[i].NodeID == nil && fallback != "" {
			entries[i].NodeID = stringPtr(fallback)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return rewriteOffloadEntries(ctx, entries)
}

func (e offloadIndexEntry) displayNodeID() string {
	if e.NodeID == nil || strings.TrimSpace(*e.NodeID) == "" {
		return "pending"
	}
	return *e.NodeID
}

func stringPtr(s string) *string {
	return &s
}

func stripMermaidFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```mermaid")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func safeTaskLabel(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	label = strings.ReplaceAll(label, "_", "-")
	label = unsafeFilenameChars.ReplaceAllString(label, "-")
	label = strings.Trim(label, "-.")
	if label == "" {
		return "task"
	}
	if len(label) > 40 {
		label = label[:40]
		label = strings.Trim(label, "-.")
	}
	return label
}

func mmdPrefixFromFile(filename string) string {
	base := filepath.Base(filename)
	if len(base) >= 3 {
		prefix := base[:3]
		if prefix[0] >= '0' && prefix[0] <= '9' &&
			prefix[1] >= '0' && prefix[1] <= '9' &&
			prefix[2] >= '0' && prefix[2] <= '9' {
			return prefix
		}
	}
	return "000"
}

func eligibleL2Entries(
	state *offloadState,
	entries []offloadIndexEntry,
) []offloadIndexEntry {
	if state == nil || state.ActiveMMDFile == "" {
		return nil
	}
	var out []offloadIndexEntry
	for _, entry := range entries {
		if entry.NodeID != nil && *entry.NodeID != offloadWaitNodeID {
			continue
		}
		if boundaryForEntry(state, entry, entries) != state.ActiveMMDFile {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func boundaryForEntry(
	state *offloadState,
	entry offloadIndexEntry,
	entries []offloadIndexEntry,
) string {
	if state == nil {
		return ""
	}
	idx := -1
	for i := range entries {
		if entries[i].ToolCallID == entry.ToolCallID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ""
	}
	var chosen offloadBoundary
	for _, boundary := range state.Boundaries {
		if boundary.StartIndex <= idx && boundary.StartIndex >= chosen.StartIndex {
			chosen = boundary
		}
	}
	if chosen.Result != offloadBoundaryLong {
		return ""
	}
	return chosen.TargetMMD
}
