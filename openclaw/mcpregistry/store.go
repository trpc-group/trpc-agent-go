//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package mcpregistry stores scoped MCP server registrations for OpenClaw.
package mcpregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const (
	// ScopeSession makes one MCP server visible only to the current session.
	ScopeSession Scope = "session"
	// ScopeChat makes one MCP server visible in the current chat container.
	ScopeChat Scope = "chat"
	// ScopeUser makes one MCP server visible to the current actor.
	ScopeUser Scope = "user"
	// ScopeWorkspace makes one MCP server visible to the current workspace.
	ScopeWorkspace Scope = "workspace"
	// ScopeGlobal makes one MCP server visible to the whole OpenClaw instance.
	ScopeGlobal Scope = "global"

	defaultRegistryFileName = "registry.json"
	defaultSecretsFileName  = "secrets.json"

	secretRedaction = "***"

	scopeKeyGlobal = "global"

	transportStdio      = "stdio"
	transportSSE        = "sse"
	transportStreamable = "streamable"
	transportHTTP       = "http"
	transportHTTPAlt    = "streamable_http"
)

var (
	errRegistryContextUnavailable = errors.New(
		"mcp registry context is unavailable",
	)
	errRegistryEntryNotFound = errors.New("mcp registry entry not found")
)

var sensitiveHeaderNames = map[string]struct{}{
	"authorization":       {},
	"cookie":              {},
	"proxy-authorization": {},
	"set-cookie":          {},
	"x-api-key":           {},
}

var sensitiveQueryHints = []string{
	"api_key",
	"apikey",
	"auth",
	"credential",
	"key",
	"password",
	"secret",
	"token",
}

// Scope identifies where one MCP server registration is visible.
type Scope string

// RuntimeContext describes the current OpenClaw turn for scope resolution.
type RuntimeContext struct {
	AppName       string
	SessionID     string
	UserID        string
	StorageUserID string
	ChatID        string
	WorkspaceID   string
}

// Entry is one persisted MCP server registration.
type Entry struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	Scope              Scope                `json:"scope"`
	ScopeKey           string               `json:"scope_key"`
	Description        string               `json:"description,omitempty"`
	Connection         mcp.ConnectionConfig `json:"connection"`
	HasSensitiveValues bool                 `json:"has_sensitive_values"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

// EntryView is the safe model-facing view of one registry entry.
type EntryView struct {
	Name               string `json:"name"`
	Scope              Scope  `json:"scope"`
	Description        string `json:"description,omitempty"`
	Selector           string `json:"selector"`
	QualifiedSelector  string `json:"qualified_selector"`
	Transport          string `json:"transport"`
	ServerURL          string `json:"server_url,omitempty"`
	Command            string `json:"command,omitempty"`
	HasSensitiveValues bool   `json:"has_sensitive_values"`
	UpdatedAt          string `json:"updated_at,omitempty"`
}

// UpsertRequest stores one MCP server registration.
type UpsertRequest struct {
	Context          RuntimeContext
	Name             string
	Scope            Scope
	Description      string
	Connection       mcp.ConnectionConfig
	ClearServerURL   bool
	ClearHeaders     bool
	ClearCommand     bool
	ClearArgs        bool
	ClearTimeout     bool
	ClearDescription bool
	UpdateOnly       bool
	AllowUpdate      bool
}

// DeleteRequest removes one MCP server registration from the current scope.
type DeleteRequest struct {
	Context RuntimeContext
	Name    string
	Scope   Scope
}

// FileStore persists registry metadata and raw sensitive connection values.
type FileStore struct {
	mu           sync.Mutex
	registryPath string
	secretsPath  string
	now          func() time.Time
}

type registryState struct {
	Entries []Entry `json:"entries"`
}

type secretState struct {
	Connections map[string]mcp.ConnectionConfig `json:"connections,omitempty"`
}

// DefaultDir returns the default registry directory under stateDir.
func DefaultDir(stateDir string) string {
	return filepath.Join(strings.TrimSpace(stateDir), "mcp")
}

// NewFileStore creates a JSON-backed MCP registry store.
func NewFileStore(dir string) *FileStore {
	dir = strings.TrimSpace(dir)
	return &FileStore{
		registryPath: filepath.Join(dir, defaultRegistryFileName),
		secretsPath:  filepath.Join(dir, defaultSecretsFileName),
		now:          time.Now,
	}
}

// Upsert creates or updates one scoped MCP server registration.
func (s *FileStore) Upsert(
	ctx context.Context,
	req UpsertRequest,
) (EntryView, error) {
	if s == nil {
		return EntryView{}, errors.New("mcp registry store is nil")
	}
	name, err := normalizeName(req.Name)
	if err != nil {
		return EntryView{}, err
	}
	scope, scopeKey, err := resolveScope(req.Scope, req.Context)
	if err != nil {
		return EntryView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, secrets, err := s.loadLocked(ctx)
	if err != nil {
		return EntryView{}, err
	}

	now := s.now().UTC()
	id := entryID(scope, scopeKey, name)
	index := findEntryIndex(state.Entries, id)
	if req.UpdateOnly && index < 0 {
		return EntryView{}, fmt.Errorf(
			"mcp_registry_update: %w: %s",
			errRegistryEntryNotFound,
			name,
		)
	}
	if !req.AllowUpdate && !req.UpdateOnly && index >= 0 {
		return EntryView{}, fmt.Errorf(
			"mcp_registry_add: entry already exists; use update: %s",
			name,
		)
	}

	description := strings.TrimSpace(req.Description)
	conn := req.Connection
	if req.UpdateOnly && index >= 0 {
		existing := state.Entries[index]
		conn = mergeConnection(
			rawConnectionForEntry(existing, secrets),
			conn,
			req,
		)
		if description == "" && !req.ClearDescription {
			description = existing.Description
		}
	}
	if strings.TrimSpace(conn.Description) == "" {
		conn.Description = description
	}
	conn, err = normalizeConnection(conn)
	if err != nil {
		return EntryView{}, err
	}

	safeConn, hasSensitive := redactConnection(conn)
	entry := Entry{
		ID:                 id,
		Name:               name,
		Scope:              scope,
		ScopeKey:           scopeKey,
		Description:        description,
		Connection:         safeConn,
		HasSensitiveValues: hasSensitive,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if index >= 0 {
		entry.CreatedAt = state.Entries[index].CreatedAt
		state.Entries[index] = entry
	} else {
		state.Entries = append(state.Entries, entry)
	}
	if hasSensitive {
		if secrets.Connections == nil {
			secrets.Connections = make(map[string]mcp.ConnectionConfig)
		}
		secrets.Connections[id] = conn
	} else if secrets.Connections != nil {
		delete(secrets.Connections, id)
	}

	if err := s.saveLocked(ctx, state, secrets); err != nil {
		return EntryView{}, err
	}
	return viewForEntry(entry, entry.Name), nil
}

// Delete removes one scoped MCP server registration.
func (s *FileStore) Delete(
	ctx context.Context,
	req DeleteRequest,
) (bool, Scope, error) {
	if s == nil {
		return false, "", errors.New("mcp registry store is nil")
	}
	name, err := normalizeName(req.Name)
	if err != nil {
		return false, "", err
	}
	scope, scopeKey, err := resolveScope(req.Scope, req.Context)
	if err != nil {
		return false, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, secrets, err := s.loadLocked(ctx)
	if err != nil {
		return false, "", err
	}

	id := entryID(scope, scopeKey, name)
	index := findEntryIndex(state.Entries, id)
	if index < 0 {
		return false, scope, nil
	}
	state.Entries = append(state.Entries[:index], state.Entries[index+1:]...)
	if secrets.Connections != nil {
		delete(secrets.Connections, id)
	}
	return true, scope, s.saveLocked(ctx, state, secrets)
}

// List returns the MCP registrations visible to the current context.
func (s *FileStore) List(
	ctx context.Context,
	runtime RuntimeContext,
) ([]EntryView, error) {
	entries, _, err := s.accessibleEntries(ctx, runtime)
	if err != nil {
		return nil, err
	}
	aliases := aliasMap(entries)
	views := make([]EntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, viewForEntry(entry, aliases[entry.ID]))
	}
	return views, nil
}

// ServerConfigs returns broker-ready MCP servers visible to the context.
func (s *FileStore) ServerConfigs(
	ctx context.Context,
	runtime RuntimeContext,
) (map[string]mcp.ConnectionConfig, error) {
	entries, secrets, err := s.accessibleEntries(ctx, runtime)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	aliases := aliasMap(entries)
	out := make(map[string]mcp.ConnectionConfig, len(entries)*2)
	for _, entry := range entries {
		conn := entry.Connection
		if raw, ok := secrets.Connections[entry.ID]; ok {
			conn = raw
		}
		conn.Description = descriptionWithScope(
			entry.Description,
			entry.Scope,
		)
		out[qualifiedName(entry)] = conn
		if alias := aliases[entry.ID]; alias == entry.Name {
			out[alias] = conn
		}
	}
	return out, nil
}

func (s *FileStore) accessibleEntries(
	ctx context.Context,
	runtime RuntimeContext,
) ([]Entry, secretState, error) {
	if s == nil {
		return nil, secretState{}, errors.New("mcp registry store is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, secrets, err := s.loadLocked(ctx)
	if err != nil {
		return nil, secretState{}, err
	}
	entries := make([]Entry, 0, len(state.Entries))
	for _, entry := range state.Entries {
		if isEntryVisible(entry, runtime) {
			entries = append(entries, entry)
		}
	}
	entries = preferExactChatEntries(entries, runtime)
	sortEntries(entries)
	return entries, secrets, nil
}

func (s *FileStore) loadLocked(
	ctx context.Context,
) (registryState, secretState, error) {
	if err := ctx.Err(); err != nil {
		return registryState{}, secretState{}, err
	}
	state, err := readJSONFile[registryState](s.registryPath)
	if err != nil {
		return registryState{}, secretState{}, err
	}
	secrets, err := readJSONFile[secretState](s.secretsPath)
	if err != nil {
		return registryState{}, secretState{}, err
	}
	if secrets.Connections == nil {
		secrets.Connections = make(map[string]mcp.ConnectionConfig)
	}
	return state, secrets, nil
}

func (s *FileStore) saveLocked(
	ctx context.Context,
	state registryState,
	secrets secretState,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sortEntries(state.Entries)
	if len(secrets.Connections) == 0 {
		if err := writeJSONFile(s.registryPath, state, 0o600); err != nil {
			return err
		}
		return removeIfExists(s.secretsPath)
	}
	if err := writeJSONFile(s.secretsPath, secrets, 0o600); err != nil {
		return err
	}
	return writeJSONFile(s.registryPath, state, 0o600)
}

func readJSONFile[T any](path string) (T, error) {
	var out T
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func writeJSONFile(path string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomically(path, data, perm)
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(perm); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	return os.Rename(tempPath, path)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func resolveScope(
	scope Scope,
	runtime RuntimeContext,
) (Scope, string, error) {
	if strings.TrimSpace(string(scope)) == "" {
		scope = ScopeSession
	}
	scope = Scope(strings.ToLower(strings.TrimSpace(string(scope))))
	switch scope {
	case ScopeSession:
		return requireScopeKey(scope, runtime.SessionID)
	case ScopeChat:
		if strings.TrimSpace(runtime.ChatID) != "" {
			return scope, strings.TrimSpace(runtime.ChatID), nil
		}
		return requireScopeKey(scope, runtime.StorageUserID)
	case ScopeUser:
		return requireScopeKey(scope, runtime.UserID)
	case ScopeWorkspace:
		return requireScopeKey(scope, runtime.WorkspaceID)
	case ScopeGlobal:
		return scope, scopeKeyGlobal, nil
	default:
		return "", "", fmt.Errorf("unsupported mcp registry scope: %s", scope)
	}
}

func requireScopeKey(scope Scope, value string) (Scope, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf(
			"%w for %s scope",
			errRegistryContextUnavailable,
			scope,
		)
	}
	return scope, value, nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("mcp registry name is required")
	}
	if strings.ContainsAny(name, "\r\n\t") {
		return "", fmt.Errorf("mcp registry name contains whitespace: %q", name)
	}
	return name, nil
}

func normalizeConnection(
	cfg mcp.ConnectionConfig,
) (mcp.ConnectionConfig, error) {
	command := strings.TrimSpace(cfg.Command)
	serverURL := strings.TrimSpace(cfg.ServerURL)
	transport := normalizeTransport(cfg.Transport, command, serverURL)
	if cfg.Timeout < 0 {
		return mcp.ConnectionConfig{}, errors.New("timeout must be non-negative")
	}
	switch transport {
	case transportStdio:
		if command == "" {
			return mcp.ConnectionConfig{}, errors.New("stdio MCP requires command")
		}
		if serverURL != "" {
			return mcp.ConnectionConfig{}, errors.New(
				"stdio MCP cannot use server_url",
			)
		}
		if len(cfg.Headers) > 0 {
			return mcp.ConnectionConfig{}, errors.New("stdio MCP cannot use headers")
		}
	case transportSSE, transportStreamable:
		if serverURL == "" {
			return mcp.ConnectionConfig{}, errors.New("HTTP MCP requires server_url")
		}
		parsed, err := url.Parse(serverURL)
		if err != nil {
			return mcp.ConnectionConfig{}, err
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return mcp.ConnectionConfig{}, errors.New(
				"HTTP MCP requires http or https URL",
			)
		}
		if strings.TrimSpace(parsed.Host) == "" {
			return mcp.ConnectionConfig{}, errors.New("HTTP MCP requires URL host")
		}
		if command != "" {
			return mcp.ConnectionConfig{}, errors.New("HTTP MCP cannot use command")
		}
	default:
		return mcp.ConnectionConfig{}, fmt.Errorf(
			"unsupported MCP transport: %s",
			cfg.Transport,
		)
	}
	return mcp.ConnectionConfig{
		Transport:   transport,
		ServerURL:   serverURL,
		Headers:     cloneStringMap(cfg.Headers),
		Command:     command,
		Args:        cloneStringSlice(cfg.Args),
		Timeout:     cfg.Timeout,
		Description: strings.TrimSpace(cfg.Description),
		ClientInfo:  cfg.ClientInfo,
	}, nil
}

func normalizeTransport(raw string, command string, serverURL string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "":
		if command != "" {
			return transportStdio
		}
		if serverURL != "" {
			return transportStreamable
		}
		return ""
	case transportHTTP, transportHTTPAlt:
		return transportStreamable
	default:
		return value
	}
}

func rawConnectionForEntry(
	entry Entry,
	secrets secretState,
) mcp.ConnectionConfig {
	if secrets.Connections != nil {
		if raw, ok := secrets.Connections[entry.ID]; ok {
			return raw
		}
	}
	return entry.Connection
}

func mergeConnection(
	base mcp.ConnectionConfig,
	update mcp.ConnectionConfig,
	req UpsertRequest,
) mcp.ConnectionConfig {
	out := mcp.ConnectionConfig{
		Transport:   base.Transport,
		ServerURL:   base.ServerURL,
		Headers:     cloneStringMap(base.Headers),
		Command:     base.Command,
		Args:        cloneStringSlice(base.Args),
		Timeout:     base.Timeout,
		Description: base.Description,
		ClientInfo:  base.ClientInfo,
	}
	if req.ClearServerURL {
		out.ServerURL = ""
	}
	if req.ClearHeaders {
		out.Headers = nil
	}
	if req.ClearCommand {
		out.Command = ""
	}
	if req.ClearArgs {
		out.Args = nil
	}
	if req.ClearTimeout {
		out.Timeout = 0
	}
	if req.ClearDescription {
		out.Description = ""
	}
	if strings.TrimSpace(update.Transport) != "" {
		out.Transport = update.Transport
	}
	if strings.TrimSpace(update.ServerURL) != "" {
		out.ServerURL = update.ServerURL
	}
	if update.Headers != nil {
		out.Headers = cloneStringMap(update.Headers)
	}
	if strings.TrimSpace(update.Command) != "" {
		out.Command = update.Command
	}
	if update.Args != nil {
		out.Args = cloneStringSlice(update.Args)
	}
	if update.Timeout != 0 {
		out.Timeout = update.Timeout
	}
	if strings.TrimSpace(update.Description) != "" {
		out.Description = update.Description
	}
	if strings.TrimSpace(update.ClientInfo.Name) != "" {
		out.ClientInfo.Name = update.ClientInfo.Name
	}
	if strings.TrimSpace(update.ClientInfo.Version) != "" {
		out.ClientInfo.Version = update.ClientInfo.Version
	}
	return out
}

func redactConnection(cfg mcp.ConnectionConfig) (mcp.ConnectionConfig, bool) {
	out := cfg
	out.Headers = cloneStringMap(cfg.Headers)
	out.Args = cloneStringSlice(cfg.Args)
	hasSensitive := false
	if out.ServerURL != "" {
		redactedURL, ok := redactURL(out.ServerURL)
		out.ServerURL = redactedURL
		hasSensitive = hasSensitive || ok
	}
	for key := range out.Headers {
		if isSensitiveHeader(key) {
			out.Headers[key] = secretRedaction
			hasSensitive = true
		}
	}
	args, ok := redactArgs(out.Args)
	out.Args = args
	hasSensitive = hasSensitive || ok
	return out, hasSensitive
}

func redactURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if isSensitiveQueryKey(key) {
			query.Set(key, secretRedaction)
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func isSensitiveHeader(name string) bool {
	_, ok := sensitiveHeaderNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func isSensitiveQueryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, hint := range sensitiveQueryHints {
		if key == hint || strings.Contains(key, hint) {
			return true
		}
	}
	return false
}

func redactArgs(args []string) ([]string, bool) {
	out := cloneStringSlice(args)
	changed := false
	redactNext := false
	for i, arg := range out {
		if redactNext {
			out[i] = secretRedaction
			changed = true
			redactNext = false
			continue
		}
		redacted, ok := redactArg(arg)
		if ok {
			out[i] = redacted
			changed = true
			continue
		}
		if isSensitiveArgName(arg) {
			redactNext = true
		}
	}
	return out, changed
}

func redactArg(arg string) (string, bool) {
	if redactedURL, ok := redactURL(arg); ok {
		return redactedURL, true
	}
	for _, sep := range []string{"=", ":"} {
		index := strings.Index(arg, sep)
		if index < 0 {
			continue
		}
		key := strings.TrimSpace(arg[:index])
		value := strings.TrimSpace(arg[index+len(sep):])
		if isSensitiveArgName(key) ||
			isSensitiveArgName(valueKey(value)) {
			return arg[:index+len(sep)] + secretRedaction, true
		}
	}
	return arg, false
}

func valueKey(value string) string {
	for _, sep := range []string{":", "="} {
		index := strings.Index(value, sep)
		if index >= 0 {
			return value[:index]
		}
	}
	return value
}

func isSensitiveArgName(name string) bool {
	name = strings.TrimLeft(strings.ToLower(strings.TrimSpace(name)), "-")
	return isSensitiveHeader(name) || isSensitiveQueryKey(name)
}

func entryID(scope Scope, key string, name string) string {
	sum := sha256.Sum256([]byte(
		string(scope) + "\x00" + key + "\x00" + name,
	))
	return hex.EncodeToString(sum[:])[:16]
}

func findEntryIndex(entries []Entry, id string) int {
	for i := range entries {
		if entries[i].ID == id {
			return i
		}
	}
	return -1
}

func isEntryVisible(entry Entry, runtime RuntimeContext) bool {
	switch entry.Scope {
	case ScopeSession:
		return entry.ScopeKey == strings.TrimSpace(runtime.SessionID)
	case ScopeChat:
		chatID := strings.TrimSpace(runtime.ChatID)
		storageUserID := strings.TrimSpace(runtime.StorageUserID)
		return entry.ScopeKey == chatID || entry.ScopeKey == storageUserID
	case ScopeUser:
		return entry.ScopeKey == strings.TrimSpace(runtime.UserID)
	case ScopeWorkspace:
		return entry.ScopeKey == strings.TrimSpace(runtime.WorkspaceID)
	case ScopeGlobal:
		return entry.ScopeKey == scopeKeyGlobal
	default:
		return false
	}
}

func preferExactChatEntries(
	entries []Entry,
	runtime RuntimeContext,
) []Entry {
	chatID := strings.TrimSpace(runtime.ChatID)
	storageUserID := strings.TrimSpace(runtime.StorageUserID)
	if chatID == "" || storageUserID == "" || chatID == storageUserID {
		return entries
	}

	exactNames := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Scope == ScopeChat && entry.ScopeKey == chatID {
			exactNames[entry.Name] = struct{}{}
		}
	}
	if len(exactNames) == 0 {
		return entries
	}

	out := entries[:0]
	for _, entry := range entries {
		if entry.Scope == ScopeChat && entry.ScopeKey == storageUserID {
			if _, ok := exactNames[entry.Name]; ok {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

func aliasMap(entries []Entry) map[string]string {
	out := make(map[string]string, len(entries))
	bareOwners := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		owner, ok := bareOwners[entry.Name]
		if !ok || scopeRank(entry.Scope) < scopeRank(owner.Scope) {
			bareOwners[entry.Name] = entry
		}
		out[entry.ID] = qualifiedName(entry)
	}
	for _, entry := range bareOwners {
		out[entry.ID] = entry.Name
	}
	return out
}

func viewForEntry(entry Entry, selector string) EntryView {
	return EntryView{
		Name:               entry.Name,
		Scope:              entry.Scope,
		Description:        entry.Description,
		Selector:           selector,
		QualifiedSelector:  qualifiedName(entry),
		Transport:          entry.Connection.Transport,
		ServerURL:          entry.Connection.ServerURL,
		Command:            entry.Connection.Command,
		HasSensitiveValues: entry.HasSensitiveValues,
		UpdatedAt:          entry.UpdatedAt.Format(time.RFC3339),
	}
}

func qualifiedName(entry Entry) string {
	return string(entry.Scope) + ":" + entry.Name
}

func descriptionWithScope(description string, scope Scope) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "MCP server registered in " + string(scope) + " scope."
	}
	return description + " Scope: " + string(scope) + "."
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i]
		right := entries[j]
		if scopeRank(left.Scope) != scopeRank(right.Scope) {
			return scopeRank(left.Scope) < scopeRank(right.Scope)
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.ScopeKey < right.ScopeKey
	})
}

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeSession:
		return 0
	case ScopeChat:
		return 1
	case ScopeUser:
		return 2
	case ScopeWorkspace:
		return 3
	case ScopeGlobal:
		return 4
	default:
		return 9
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneStringSlice(input []string) []string {
	if input == nil {
		return nil
	}
	out := make([]string, len(input))
	copy(out, input)
	return out
}
