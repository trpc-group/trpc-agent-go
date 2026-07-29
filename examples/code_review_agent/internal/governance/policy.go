//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package governance provides fail-closed review tool governance.
package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const maxArgumentsBytes = 16 << 10

var allowedWorkspaceCommands = map[string]string{
	"go test ./...":     "allow-go-test",
	"go vet ./...":      "allow-go-vet",
	"staticcheck ./...": "allow-staticcheck",
}

// TrustedScript is immutable Skill script source staged by the caller into a
// fresh review workspace.
type TrustedScript struct {
	Name    string
	Content []byte
}

// ScriptAsset is a verified immutable script and its generated command path.
type ScriptAsset struct {
	Command string
	Digest  [sha256.Size]byte
	Content []byte
}

// Policy allows only fixed review operations on explicitly trusted tools.
type Policy struct {
	trustedTools   map[string]tool.Tool
	trustedScripts map[string]ScriptAsset
}

// NewPolicy constructs a policy for exact tool instances and immutable Skill scripts.
func NewPolicy(trustedTools []tool.Tool, trustedScripts ...TrustedScript) (*Policy, error) {
	policy := &Policy{
		trustedTools:   make(map[string]tool.Tool, len(trustedTools)),
		trustedScripts: make(map[string]ScriptAsset, len(trustedScripts)),
	}
	for _, trustedTool := range trustedTools {
		if trustedTool == nil || trustedTool.Declaration() == nil || trustedTool.Declaration().Name == "" {
			return nil, errors.New("new governance policy: invalid trusted tool")
		}
		name := trustedTool.Declaration().Name
		if _, exists := policy.trustedTools[name]; exists {
			return nil, errors.New("new governance policy: duplicate trusted tool")
		}
		policy.trustedTools[name] = trustedTool
	}
	for _, script := range trustedScripts {
		if !validTrustedScriptName(script.Name) || len(script.Content) == 0 || len(script.Content) > 64<<10 {
			return nil, errors.New("new governance policy: invalid trusted script")
		}
		digest := sha256.Sum256(script.Content)
		command := fmt.Sprintf(".review-trusted/%x/%s", digest[:8], path.Base(script.Name))
		if _, exists := policy.trustedScripts[command]; exists {
			return nil, errors.New("new governance policy: duplicate trusted script")
		}
		policy.trustedScripts[command] = ScriptAsset{
			Command: command,
			Digest:  digest,
			Content: append([]byte(nil), script.Content...),
		}
	}
	return policy, nil
}

// ScriptAssets returns deterministic copies for read-only workspace staging.
func (p *Policy) ScriptAssets() []ScriptAsset {
	if p == nil {
		return nil
	}
	commands := make([]string, 0, len(p.trustedScripts))
	for command := range p.trustedScripts {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	assets := make([]ScriptAsset, 0, len(commands))
	for _, command := range commands {
		asset := p.trustedScripts[command]
		asset.Content = append([]byte(nil), asset.Content...)
		assets = append(assets, asset)
	}
	return assets
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *Policy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	decision, _, err := p.evaluate(ctx, req)
	return decision, err
}

func (p *Policy) evaluate(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, string, error) {
	if ctx == nil {
		return tool.DenyPermission("permission context is missing"), "deny-nil-context", nil
	}
	if err := ctx.Err(); err != nil {
		return tool.DenyPermission("permission check was canceled"), "deny-canceled", err
	}
	if p == nil || req == nil {
		return tool.DenyPermission("permission request is invalid"), "deny-invalid-request", nil
	}
	if !p.trustedRequest(req) {
		return tool.DenyPermission("tool identity is not trusted"), "deny-tool-identity", nil
	}
	switch req.ToolName {
	case "skill_load", "skill_list_docs", "skill_select_docs":
		return tool.AllowPermission(), "allow-skill-read", nil
	case "workspace_exec":
		return p.evaluateWorkspace(req.Arguments)
	case "skill_run", "skill_exec", "skill_write_stdin", "skill_poll_session",
		"skill_kill_session", "workspace_write_stdin", "workspace_kill_session":
		return tool.DenyPermission("interactive or arbitrary execution is disabled"),
			"deny-interactive-execution", nil
	default:
		return tool.DenyPermission("tool is not in the review allowlist"), "deny-unknown-tool", nil
	}
}

type workspaceInput struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Stdin         string            `json:"stdin,omitempty"`
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMS       *int              `json:"yieldMs,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func (p *Policy) evaluateWorkspace(arguments []byte) (tool.PermissionDecision, string, error) {
	if len(arguments) == 0 || len(arguments) > maxArgumentsBytes {
		return tool.DenyPermission("workspace arguments are invalid"), "deny-invalid-arguments", nil
	}
	if duplicate, err := hasDuplicateTopLevelKey(arguments); err != nil || duplicate {
		return tool.DenyPermission("workspace arguments are invalid"), "deny-duplicate-arguments", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	var input workspaceInput
	if err := decoder.Decode(&input); err != nil {
		return tool.DenyPermission("workspace arguments are invalid"), "deny-invalid-arguments", nil
	}
	if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
		return tool.DenyPermission("workspace arguments contain trailing data"),
			"deny-trailing-arguments", nil
	}
	if input.Command == "" || input.Command != strings.TrimSpace(input.Command) ||
		(input.Cwd != "" && input.Cwd != ".") || len(input.Env) != 0 || input.Stdin != "" ||
		input.Background || boolValue(input.TTY) || boolValue(input.PTY) ||
		(input.YieldTimeMS != nil && input.YieldMS != nil) ||
		!boundedOptionalMilliseconds(input.YieldTimeMS) || !boundedOptionalMilliseconds(input.YieldMS) ||
		!boundedTimeout(input) {
		return tool.DenyPermission("workspace execution options are not allowed"),
			"deny-workspace-options", nil
	}
	if unsafeCommandText(input.Command) {
		return tool.DenyPermission("shell composition is not allowed"), "deny-shell-composition", nil
	}
	if rule, ok := allowedWorkspaceCommands[input.Command]; ok {
		return tool.AllowPermission(), rule, nil
	}
	if _, ok := p.trustedScripts[input.Command]; ok {
		return tool.AllowPermission(), "allow-trusted-skill-script", nil
	}
	if dependencyInstallation(input.Command) {
		return tool.AskPermission("dependency installation requires approval"),
			"ask-dependency-installation", nil
	}
	return tool.DenyPermission("command is not in the review allowlist"), "deny-unknown-command", nil
}

func validTrustedScriptName(name string) bool {
	if path.Base(name) != name || !strings.HasSuffix(name, ".sh") {
		return false
	}
	for _, character := range name {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func boundedOptionalMilliseconds(value *int) bool {
	return value == nil || (*value >= 0 && *value <= 30_000)
}

func boundedTimeout(input workspaceInput) bool {
	values := make([]int, 0, 3)
	if input.Timeout != 0 {
		values = append(values, input.Timeout)
	}
	for _, value := range []*int{input.TimeoutSec, input.TimeoutSecOld} {
		if value != nil {
			values = append(values, *value)
		}
	}
	if len(values) != 1 {
		return false
	}
	for _, value := range values {
		if value < 1 || value > 120 {
			return false
		}
	}
	return true
}

func (p *Policy) trustedRequest(req *tool.PermissionRequest) bool {
	if req.Tool == nil || req.Declaration == nil || req.ToolName == "" {
		return false
	}
	expected, ok := p.trustedTools[req.ToolName]
	if !ok || !sameTool(expected, req.Tool) {
		return false
	}
	declaration := req.Tool.Declaration()
	if declaration == nil || declaration.Name != req.ToolName || req.Declaration.Name != req.ToolName {
		return false
	}
	return reflect.DeepEqual(req.Metadata, tool.MetadataOf(req.Tool))
}

func (p *Policy) visibleTool(candidate tool.Tool) bool {
	if p == nil || candidate == nil || candidate.Declaration() == nil {
		return false
	}
	name := candidate.Declaration().Name
	req := &tool.PermissionRequest{
		Tool:        candidate,
		ToolName:    name,
		Declaration: candidate.Declaration(),
		Metadata:    tool.MetadataOf(candidate),
	}
	if !p.trustedRequest(req) {
		return false
	}
	switch name {
	case "skill_load", "skill_list_docs", "skill_select_docs", "workspace_exec":
		return true
	default:
		return false
	}
}

func sameTool(left, right tool.Tool) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() ||
		!leftValue.Type().Comparable() {
		return false
	}
	return leftValue.Interface() == rightValue.Interface()
}

func hasDuplicateTopLevelKey(arguments []byte) (bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	tokenValue, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delimiter, ok := tokenValue.(json.Delim)
	if !ok || delimiter != '{' {
		return false, errors.New("arguments are not an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return false, errors.New("argument key is not a string")
		}
		if _, exists := seen[key]; exists {
			return true, nil
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false, err
		}
	}
	_, err = decoder.Token()
	return false, err
}

func unsafeCommandText(command string) bool {
	return strings.ContainsAny(command, "|;&><`$(){}*?\\\r\n") || strings.Contains(command, "$(") ||
		strings.Contains(command, "${")
}

func dependencyInstallation(command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 3 {
		return false
	}
	if fields[0] == "go" && fields[1] == "install" {
		return true
	}
	if fields[0] == "go" && fields[1] == "get" {
		return true
	}
	return len(fields) == 3 && fields[0] == "go" && fields[1] == "mod" && fields[2] == "download"
}

var _ tool.PermissionPolicy = (*Policy)(nil)

var errDecisionNotRecorded = errors.New("governance decision was not recorded")
