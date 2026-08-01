//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRedactValueAndJSON_Branches(t *testing.T) {
	t.Parallel()
	require.Nil(t, RedactMap(nil))
	require.Nil(t, RedactValue(nil))
	require.Equal(t, 7, RedactValue(7))
	require.Equal(t, []byte(nil), RedactJSON(nil))
	require.Equal(t, []byte{}, RedactJSON([]byte{}))

	token := "sk-" + strings.Repeat("c", 32)
	raw := RedactJSON([]byte(`{"a":"` + token + `","arr":["` + token + `"]}`))
	require.NotContains(t, string(raw), token)

	got := RedactValue(map[string]any{
		"s": token,
		"n": 1,
		"m": map[string]any{"x": token},
		"l": []any{token, 2},
	}).(map[string]any)
	require.NotContains(t, got["s"], token)
	require.Equal(t, 1, got["n"])

	b := RedactValue([]byte(`{"k":"` + token + `"}`)).([]byte)
	require.NotContains(t, string(b), token)
	rm := RedactValue(json.RawMessage(`"` + token + `"`)).(json.RawMessage)
	require.NotContains(t, string(rm), token)

	plain := RedactJSON([]byte("Bearer " + token))
	require.NotContains(t, string(plain), token)
}

func TestAfterToolRedact_MoreShapes(t *testing.T) {
	t.Parallel()
	cb := AfterToolRedact()
	res, err := cb(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, res)

	res, err = cb(context.Background(), &tool.AfterToolArgs{Result: nil})
	require.NoError(t, err)
	require.Nil(t, res)

	token := "sk-" + strings.Repeat("d", 32)
	res, err = cb(context.Background(), &tool.AfterToolArgs{
		Result: map[string]any{"stdout": "Bearer " + token},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	m := res.CustomResult.(map[string]any)
	require.NotContains(t, m["stdout"], token)

	res, err = cb(context.Background(), &tool.AfterToolArgs{
		Result: []any{"Bearer " + token},
	})
	require.NoError(t, err)
	require.NotContains(t, res.CustomResult.([]any)[0], token)

	res, err = cb(context.Background(), &tool.AfterToolArgs{
		Result: []byte("Bearer " + token),
	})
	require.NoError(t, err)
	require.NotContains(t, string(res.CustomResult.([]byte)), token)

	res, err = cb(context.Background(), &tool.AfterToolArgs{
		Result: json.RawMessage(`"Bearer ` + token + `"`),
	})
	require.NoError(t, err)
	require.NotContains(t, string(res.CustomResult.(json.RawMessage)), token)
}

type errPolicy struct{}

func (errPolicy) CheckToolPermission(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
	return tool.PermissionDecision{}, errors.New("boom")
}

type allowOnce struct{}

func (allowOnce) CheckToolPermission(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
	return tool.AllowPermission(), nil
}

func TestCompose_NilEmptyErrorAllow(t *testing.T) {
	t.Parallel()
	p := Compose(nil, nil)
	dec, err := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "t",
		Arguments: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, dec.Action)

	_, err = Compose(allowOnce{}, errPolicy{}).CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "t",
		Arguments: []byte(`{"q":"x"}`),
	})
	require.Error(t, err)

	dec, err = Compose(allowOnce{}, allowOnce{}).CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "t",
		Arguments: []byte(`{"q":"x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, dec.Action)
}

func TestRuleHelpers_EmptyAndNil(t *testing.T) {
	t.Parallel()
	require.Equal(t, "extra.rule", RuleFunc(nil).ID())
	f, ok := RuleFunc(nil).Check(Extracted{}, Policy{})
	require.False(t, ok)
	require.Equal(t, Finding{}, f)

	r := NamedRule("site.x", nil)
	require.Equal(t, "site.x", r.ID())
	f, ok = r.Check(Extracted{}, Policy{})
	require.False(t, ok)

	f, ok = DenyToolNames().Check(Extracted{ToolName: "x"}, Policy{})
	require.False(t, ok)
	f, ok = AskToolNames(" ").Check(Extracted{ToolName: "x"}, Policy{})
	require.False(t, ok)
	f, ok = DenyCommandSubstrings(" ", "").Check(Extracted{Command: "echo"}, Policy{})
	require.False(t, ok)

	f, ok = DenyToolNames("Host_Exec").Check(Extracted{ToolName: "host_exec"}, Policy{})
	require.True(t, ok)
	require.Equal(t, DecisionDeny, f.Decision)
}

func TestAuditAndPolicy_Edges(t *testing.T) {
	t.Parallel()
	require.NoError(t, (*MemoryAuditor)(nil).Append(AuditEvent{}))
	require.Nil(t, (*MemoryAuditor)(nil).Events())

	_, err := NewFileAuditor("")
	require.Error(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	fa, err := NewFileAuditor(path)
	require.NoError(t, err)
	require.NoError(t, fa.Append(AuditEvent{ToolName: "t", Decision: DecisionAllow, RuleID: "allow"}))
	require.NoError(t, (*FileAuditor)(nil).Append(AuditEvent{}))

	require.Error(t, WriteReportJSON(filepath.Join(dir, "missing", "x.json"), nil))
	require.NoError(t, WriteReportJSON(filepath.Join(dir, "r.json"), []Result{{Decision: DecisionAllow}}))

	require.Equal(t, []string{"a"}, cleanStrings([]string{" a ", "", "  "}))
}

func TestLooksLikeRemoteGoPkg_Edges(t *testing.T) {
	t.Parallel()
	require.False(t, looksLikeRemoteGoPkg(""))
	require.False(t, looksLikeRemoteGoPkg("./main.go"))
	require.False(t, looksLikeRemoteGoPkg("../x"))
	require.False(t, looksLikeRemoteGoPkg("/abs"))
	require.False(t, looksLikeRemoteGoPkg(`\abs`))
	require.False(t, looksLikeRemoteGoPkg(`C:\x`))
	require.False(t, looksLikeRemoteGoPkg("main.go"))
	require.True(t, looksLikeRemoteGoPkg("github.com/a/b"))
}

func TestPathCandidates_WindowsHostForm(t *testing.T) {
	t.Parallel()
	got := pathCandidates("file://localhost/home/u/.ssh/id_rsa")
	require.Contains(t, got, "/home/u/.ssh/id_rsa")
	got = pathCandidates("file://server/share/key")
	require.NotEmpty(t, got)
}

func TestGuard_NilAndEmptyExtras(t *testing.T) {
	t.Parallel()
	require.Equal(t, DefaultPolicy(), (*Guard)(nil).Policy())
	require.Nil(t, (*Guard)(nil).LastResults())

	g := NewGuard(WithExtraRules(nil, DenyToolNames("web_search")))
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "web_search",
		Arguments: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
}
