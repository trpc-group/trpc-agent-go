//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// covercoreNoAuditPolicy returns a valid policy with file audit disabled
// so no audit file appears in the working directory.
func covercoreNoAuditPolicy() Policy {
	p := DefaultPolicy()
	p.Audit.Path = ""
	p.Audit.Required = false
	return p
}

// TestCovercore_GuardOptions covers the option setters and NewGuard error
// paths.
func TestCovercore_GuardOptions(t *testing.T) {
	// A nil option is skipped.
	g, err := NewGuard(nil, WithPolicy(covercoreNoAuditPolicy()))
	require.NoError(t, err)
	require.NoError(t, g.Close())

	// A nil audit writer fails the option.
	_, err = NewGuard(WithAuditWriter(nil))
	require.ErrorContains(t, err, "audit writer is nil")

	// An invalid in-memory policy fails validation.
	bad := covercoreNoAuditPolicy()
	bad.Version = 2
	_, err = NewGuard(WithPolicy(bad))
	require.ErrorContains(t, err, "version must be 1")

	// A missing policy file fails to load.
	_, err = NewGuard(WithPolicyFile(t.TempDir() + "/missing.yaml"))
	require.ErrorContains(t, err, "load policy")

	// An audit path that cannot be opened fails construction.
	badAudit := covercoreNoAuditPolicy()
	badAudit.Audit.Path = t.TempDir() + "/no/such/dir/audit.jsonl"
	_, err = NewGuard(WithPolicy(badAudit))
	require.ErrorContains(t, err, "open audit path")

	// WithRedaction and WithConcurrencyPolicy are accepted.
	g, err = NewGuard(
		WithPolicy(covercoreNoAuditPolicy()),
		WithRedaction(false),
		WithConcurrencyPolicy(ConcurrencyPolicy{MaxActiveCalls: 2}),
	)
	require.NoError(t, err)
	require.False(t, g.redaction)
	require.NoError(t, g.Close())
}

// TestCovercore_GuardPolicyAccessor verifies Policy returns the loaded
// policy.
func TestCovercore_GuardPolicyAccessor(t *testing.T) {
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()))
	require.NoError(t, err)
	defer g.Close()
	p := g.Policy()
	require.Equal(t, 1, p.Version)
	require.Equal(t, 30*time.Second, p.MaxTimeout)
}

// TestCovercore_GuardNilReceivers covers the nil-guard branches.
func TestCovercore_GuardNilReceivers(t *testing.T) {
	var g *Guard
	_, err := g.Scan(context.Background(), ScanInput{ToolName: "t"})
	require.ErrorContains(t, err, "guard is nil")

	_, err = g.ScanBatch(context.Background(), []ScanInput{{ToolName: "t"}})
	require.ErrorContains(t, err, "guard is nil")

	decision, err := g.CheckToolPermission(context.Background(),
		&tool.PermissionRequest{ToolName: "t"})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "guard is nil")

	require.NoError(t, g.Close())
}

// TestCovercore_CheckToolPermissionNilRequest covers the nil request deny.
func TestCovercore_CheckToolPermissionNilRequest(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "permission request is nil")
}

// TestCovercore_CheckToolPermissionUnknownToolMalformed covers the ask
// path for an unknown tool with malformed arguments.
func TestCovercore_CheckToolPermissionUnknownToolMalformed(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "totally_unknown_tool",
		Arguments: []byte(`{broken`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.unknown_malformed")
}

// TestCovercore_CheckToolPermissionMetadataMapping verifies that tool
// metadata is mapped into the scan input and that a large MaxResultSize
// becomes the output-size hint.
func TestCovercore_CheckToolPermissionMetadataMapping(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls"}`),
		Metadata: tool.ToolMetadata{
			ReadOnly:      true,
			MaxResultSize: 4096,
		},
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// TestCovercore_CheckToolPermissionConcurrencyExceeded covers the
// concurrency gate deny path and the after-tool release.
func TestCovercore_CheckToolPermissionConcurrencyExceeded(t *testing.T) {
	guard := newTestGuard(t, WithConcurrencyPolicy(ConcurrencyPolicy{MaxActiveCalls: 1}))
	allowArgs := []byte(`{"command":"ls"}`)

	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call-1",
		Arguments:  allowArgs,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	// The second in-flight call exceeds the global cap and is denied.
	decision, err = guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call-2",
		Arguments:  allowArgs,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "resource.concurrency_exceeded")

	// Running the after-tool callback for call-1 frees the slot.
	cbs := guard.Callbacks()
	_, err = cbs.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName:   "workspace_exec",
		ToolCallID: "call-1",
		Arguments:  allowArgs,
		Result:     "ok",
	})
	require.NoError(t, err)

	decision, err = guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call-3",
		Arguments:  allowArgs,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// TestCovercore_ApplyProfileDefaults covers the profile default backfill
// branches.
func TestCovercore_ApplyProfileDefaults(t *testing.T) {
	guard := newTestGuard(t)

	// Backend and timeout filled from the named profile; the profile
	// default (5m) is capped at the policy max (30s).
	in := guard.applyProfileDefaults(ScanInput{ToolName: "x", ToolProfile: "exec_command"})
	require.Equal(t, BackendHostExec, in.Backend)
	require.Equal(t, 30*time.Second, in.Timeout)

	// An explicit backend and timeout are preserved.
	in = guard.applyProfileDefaults(ScanInput{
		ToolName: "x", ToolProfile: "exec_command",
		Backend: BackendMCP, Timeout: 5 * time.Second,
	})
	require.Equal(t, BackendMCP, in.Backend)
	require.Equal(t, 5*time.Second, in.Timeout)

	// Unknown profile: nothing changes.
	in = guard.applyProfileDefaults(ScanInput{ToolName: "x", ToolProfile: "nope", Backend: BackendMCP})
	require.Equal(t, BackendMCP, in.Backend)
	require.Zero(t, in.Timeout)

	// Empty backend falls back to a tool-name profile lookup.
	in = guard.applyProfileDefaults(ScanInput{ToolName: "workspace_exec"})
	require.Equal(t, BackendWorkspaceExec, in.Backend)
	require.Equal(t, "workspace_exec", in.ToolProfile)
	require.Equal(t, 30*time.Second, in.Timeout)

	// Empty backend with no matching profile stays unknown-ish.
	in = guard.applyProfileDefaults(ScanInput{ToolName: "no_such_tool"})
	require.Empty(t, in.Backend)
}

// TestCovercore_CapTimeout covers the timeout-cap branches.
func TestCovercore_CapTimeout(t *testing.T) {
	require.Zero(t, capTimeout(0, 30*time.Second))
	require.Zero(t, capTimeout(-time.Second, 30*time.Second))
	require.Equal(t, 10*time.Second, capTimeout(10*time.Second, 0))
	require.Equal(t, 30*time.Second, capTimeout(time.Minute, 30*time.Second))
	require.Equal(t, 10*time.Second, capTimeout(10*time.Second, 30*time.Second))
}

// TestCovercore_StashPopSideTables covers the scan-event and release side
// tables including the post-Close map reinitialization.
func TestCovercore_StashPopSideTables(t *testing.T) {
	guard := newTestGuard(t)

	// Empty ids are ignored.
	guard.stashScanEvent("", ScanEvent{ScanID: "x"})
	guard.stashRelease("", func() {})
	require.Empty(t, guard.popScanEvent("").ScanID)
	require.Nil(t, guard.popRelease(""))

	// stashRelease ignores a nil release function.
	guard.stashRelease("nil-rel", nil)
	require.Nil(t, guard.popRelease("nil-rel"))

	// Round-trip a release function.
	ran := false
	guard.stashRelease("rel", func() { ran = true })
	release := guard.popRelease("rel")
	require.NotNil(t, release)
	release()
	require.True(t, ran)
	require.Nil(t, guard.popRelease("rel"))

	// After Close the maps are nil; stashScanEvent reinitializes.
	require.NoError(t, guard.Close())
	guard.stashScanEvent("after-close", ScanEvent{ScanID: "s"})
	require.Equal(t, "s", guard.popScanEvent("after-close").ScanID)
}

// TestCovercore_AttachCallbacks covers merging into existing callbacks and
// the nil-callbacks guard.
func TestCovercore_AttachCallbacks(t *testing.T) {
	guard := newTestGuard(t)
	require.NotPanics(t, func() { guard.AttachCallbacks(nil) })

	cbs := tool.NewCallbacks()
	guard.AttachCallbacks(cbs)
	require.NotEmpty(t, cbs.AfterTool)

	// The nil-args invocation of the registered callback is a no-op.
	out, err := cbs.AfterTool[0](context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, out)
}

// TestCovercore_AfterToolErrorWithSecret covers error redaction and the
// post_execute audit event for a failed call.
func TestCovercore_AfterToolErrorWithSecret(t *testing.T) {
	auditBuf := new(bytes.Buffer)
	guard := newTestGuard(t, WithAuditWriter(auditBuf))
	cbs := guard.Callbacks()

	args := &tool.AfterToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls"}`),
		Error:     errors.New("dial failed: API_KEY=sk_live_1234567890abcdef1234"),
	}
	out, err := cbs.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, out)

	// The result was replaced with a structured redacted error.
	replaced, ok := out.CustomResult.(map[string]any)
	require.True(t, ok, "CustomResult=%T", out.CustomResult)
	require.Equal(t, "error_redacted", replaced["status"])
	msg, _ := replaced["message"].(string)
	require.NotContains(t, msg, "sk_live_1234567890abcdef1234")
	require.Contains(t, msg, "[REDACTED:")

	// The post_execute audit event records execution=error.
	require.Contains(t, auditBuf.String(), `"execution":"error"`)
}

// TestCovercore_AfterToolErrorWithoutSecret covers the non-secret error
// pass-through.
func TestCovercore_AfterToolErrorWithoutSecret(t *testing.T) {
	auditBuf := new(bytes.Buffer)
	guard := newTestGuard(t, WithAuditWriter(auditBuf))
	cbs := guard.Callbacks()

	args := &tool.AfterToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls"}`),
		Error:     errors.New("plain failure"),
		Result:    "partial",
	}
	out, err := cbs.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	// The guard callback made no change, so the result passes through
	// untouched.
	if out != nil && out.CustomResult != nil {
		require.Equal(t, "partial", out.CustomResult)
	}
	require.Contains(t, auditBuf.String(), `"execution":"error"`)
}

// TestCovercore_RedactErrorIfNeededBranches covers the disabled and
// no-error early returns.
func TestCovercore_RedactErrorIfNeededBranches(t *testing.T) {
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()), WithRedaction(false))
	require.NoError(t, err)
	defer g.Close()

	args := &tool.AfterToolArgs{Error: errors.New("API_KEY=sk_live_1234567890abcdef1234")}
	require.False(t, g.redactErrorIfNeeded(args))

	require.False(t, g.redactErrorIfNeeded(&tool.AfterToolArgs{}))
}

// TestCovercore_RedactMetaIfNeeded covers the meta redaction branches.
func TestCovercore_RedactMetaIfNeeded(t *testing.T) {
	guard := newTestGuard(t)

	// Empty meta is a no-op.
	require.False(t, guard.redactMetaIfNeeded(&tool.AfterToolArgs{}))

	// Meta with a secret value is redacted in place.
	args := &tool.AfterToolArgs{Meta: map[string]any{
		"token": "xoxb-1234567890-abcdef",
		"clean": "value",
	}}
	require.True(t, guard.redactMetaIfNeeded(args))
	require.NotContains(t, args.Meta["token"], "xoxb-1234567890-abcdef")
	require.Equal(t, "value", args.Meta["clean"])

	// Meta without secrets reports no change.
	args = &tool.AfterToolArgs{Meta: map[string]any{"clean": "value"}}
	require.False(t, guard.redactMetaIfNeeded(args))

	// Redaction disabled is a no-op.
	g2, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()), WithRedaction(false))
	require.NoError(t, err)
	defer g2.Close()
	args = &tool.AfterToolArgs{Meta: map[string]any{"token": "xoxb-1234567890-abcdef"}}
	require.False(t, g2.redactMetaIfNeeded(args))
}

// TestCovercore_TrackSessionLifecycle covers session registration and kill
// tracking through the after-tool callback.
func TestCovercore_TrackSessionLifecycle(t *testing.T) {
	guard := newTestGuard(t)
	cbs := guard.Callbacks()

	_, err := cbs.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"ls"}`),
		Result:    map[string]any{"session_id": "sess-42"},
	})
	require.NoError(t, err)
	require.True(t, guard.sessions.isKnown("sess-42"))
	require.False(t, guard.sessions.isKilled("sess-42"))

	_, err = cbs.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName:  "kill_session",
		Arguments: []byte(`{"session_id":"sess-42"}`),
		Result:    map[string]any{"session_id": "sess-42"},
	})
	require.NoError(t, err)
	require.True(t, guard.sessions.isKilled("sess-42"))

	// An unrelated tool name with a session id changes nothing.
	_, err = cbs.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName:  "read_file",
		Arguments: []byte(`{}`),
		Result:    map[string]any{"session_id": "sess-99"},
	})
	require.NoError(t, err)
	require.False(t, guard.sessions.isKnown("sess-99"))

	// A nil result has no session to track.
	guard.trackSessionLifecycle("exec_command", nil)
	require.False(t, guard.sessions.isKnown(""))
}

// TestCovercore_ExtractSessionID covers the result-shape branches.
func TestCovercore_ExtractSessionID(t *testing.T) {
	require.Empty(t, extractSessionID(nil))
	require.Empty(t, extractSessionID("not a map"))
	require.Empty(t, extractSessionID(map[string]any{"other": 1}))
	require.Empty(t, extractSessionID(map[string]any{"session_id": 42}))
	require.Equal(t, "a", extractSessionID(map[string]any{"session_id": "a"}))
	require.Equal(t, "b", extractSessionID(map[string]any{"sessionId": "b"}))
}

// TestCovercore_PostExecuteSessionHashFallback covers the decode-failure
// fallback to the result session id.
func TestCovercore_PostExecuteSessionHashFallback(t *testing.T) {
	guard := newTestGuard(t)

	// Malformed arguments: the decoder fails and the result session id
	// is hashed instead.
	args := &tool.AfterToolArgs{
		ToolName:  "exec_command",
		Arguments: []byte(`{broken`),
		Result:    map[string]any{"session_id": "sess-7"},
	}
	require.Equal(t, hashSessionID("sess-7"), guard.postExecuteSessionHash(args))

	// No session anywhere yields an empty hash.
	args = &tool.AfterToolArgs{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"ls"}`),
		Result:    "plain",
	}
	require.Empty(t, guard.postExecuteSessionHash(args))

	// Nil args yield an empty hash.
	require.Empty(t, guard.postExecuteSessionHash(nil))
}

// TestCovercore_RedactAndLimitTrackedNoRedaction verifies size limiting
// still applies when redaction is disabled.
func TestCovercore_RedactAndLimitTrackedNoRedaction(t *testing.T) {
	p := covercoreNoAuditPolicy()
	p.MaxOutputSize = 64
	g, err := NewGuard(WithPolicy(p), WithRedaction(false))
	require.NoError(t, err)
	defer g.Close()

	safe, changed, truncated, size := g.redactAndLimitTracked(strings.Repeat("y", 512))
	require.True(t, truncated)
	require.True(t, changed)
	require.LessOrEqual(t, size, int64(64))
	s, ok := safe.(string)
	require.True(t, ok)
	require.Contains(t, s, "[truncated:tool_safety]")
}

// TestCovercore_RedactStringValueDisabled covers the disabled-redaction
// pass-through of the public helpers.
func TestCovercore_RedactStringValueDisabled(t *testing.T) {
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()), WithRedaction(false))
	require.NoError(t, err)
	defer g.Close()

	out, changed := g.RedactString("AKIAIOSFODNN7EXAMPLE")
	require.False(t, changed)
	require.Equal(t, "AKIAIOSFODNN7EXAMPLE", out)

	v, changed, err := g.RedactValue(map[string]any{"k": "AKIAIOSFODNN7EXAMPLE"})
	require.NoError(t, err)
	require.False(t, changed)
	require.NotNil(t, v)
}

// TestCovercore_BackendFor covers the profile-backend lookup.
func TestCovercore_BackendFor(t *testing.T) {
	guard := newTestGuard(t)
	require.Equal(t, BackendHostExec, guard.backendFor("exec_command"))
	require.Equal(t, BackendUnknown, guard.backendFor("no_such_tool"))
}

// TestCovercore_FormatReason covers the no-findings fallback.
func TestCovercore_FormatReason(t *testing.T) {
	reason := formatReason(ScanReport{Decision: DecisionDeny, RiskLevel: RiskHigh})
	require.Contains(t, reason, "rule=unknown")

	reason = formatReason(ScanReport{
		Decision:  DecisionDeny,
		RiskLevel: RiskHigh,
		Findings:  []Finding{{RuleID: "r1", Recommendation: "stop"}},
	})
	require.Contains(t, reason, "rule=r1")
	require.Contains(t, reason, "recommendation=stop")
}

// TestCovercore_CoalesceBackend covers the backend preference order.
func TestCovercore_CoalesceBackend(t *testing.T) {
	require.Equal(t, BackendHostExec, coalesceBackend(BackendHostExec, BackendWorkspaceExec))
	require.Equal(t, BackendHostExec, coalesceBackend(BackendUnknown, BackendHostExec))
	require.Equal(t, BackendWorkspaceExec, coalesceBackend("", BackendWorkspaceExec))
	require.Equal(t, BackendUnknown, coalesceBackend(BackendUnknown, BackendUnknown))
	require.Empty(t, coalesceBackend("", ""))
}

// TestCovercore_GuardCloseKeepsInjectedWriter verifies Close does not
// close a caller-provided audit writer.
func TestCovercore_GuardCloseKeepsInjectedWriter(t *testing.T) {
	buf := new(bytes.Buffer)
	g, err := NewGuard(
		WithPolicy(covercoreNoAuditPolicy()),
		WithAuditWriter(buf),
	)
	require.NoError(t, err)
	require.NoError(t, g.Close())
	// The injected writer survives Close and still accepts events.
	require.NotNil(t, g.audit)
	require.NoError(t, g.audit.Append(AuditEvent{ScanID: "post-close"}))
	require.Contains(t, buf.String(), "post-close")
}

// TestCovercore_RedactArtifactGuard covers the public RedactArtifact with
// redaction enabled and disabled.
func TestCovercore_RedactArtifactGuard(t *testing.T) {
	guard := newTestGuard(t)
	out, changed, err := guard.RedactArtifact(&artifact.Artifact{
		MimeType: "text/plain",
		Data:     []byte("token: AKIAIOSFODNN7EXAMPLE"),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(out.Data), "AKIAIOSFODNN7EXAMPLE")

	g2, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()), WithRedaction(false))
	require.NoError(t, err)
	defer g2.Close()
	in := &artifact.Artifact{MimeType: "text/plain", Data: []byte("x")}
	out, changed, err = g2.RedactArtifact(in)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, in, out)
}

// TestCovercore_IsTextMIME covers the MIME classification branches.
func TestCovercore_IsTextMIME(t *testing.T) {
	require.False(t, isTextMIME(""))
	require.False(t, isTextMIME("   "))
	require.True(t, isTextMIME("text/plain"))
	require.True(t, isTextMIME(" TEXT/HTML "))
	require.True(t, isTextMIME("application/json"))
	require.True(t, isTextMIME("application/x-yaml"))
	require.True(t, isTextMIME("application/x-sh"))
	require.True(t, isTextMIME("application/hal+json"))
	require.True(t, isTextMIME("application/vnd.api+yaml"))
	require.False(t, isTextMIME("application/octet-stream"))
	require.False(t, isTextMIME("image/png"))
}

// TestCovercore_RedactArtifactNameURL covers the name/URL redaction that
// applies regardless of MIME type.
func TestCovercore_RedactArtifactNameURL(t *testing.T) {
	// Text artifact with a secret only in the name.
	in := &artifact.Artifact{
		MimeType: "text/plain",
		Name:     "dump-AKIAIOSFODNN7EXAMPLE.txt",
		Data:     []byte("clean"),
	}
	out, changed, err := redactArtifact(in)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, out.Name, "AKIAIOSFODNN7EXAMPLE")
	require.Equal(t, []byte("clean"), out.Data)

	// Binary artifact with clean data but a secret in the URL is
	// redacted, not rejected.
	in = &artifact.Artifact{
		MimeType: "application/octet-stream",
		URL:      "https://user:xoxb-1234567890-abcdef@example.com/f",
		Data:     []byte{0x1, 0x2},
	}
	out, changed, err = redactArtifact(in)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, out.URL, "xoxb-1234567890-abcdef")
}

// covercoreArtifactService is a controllable artifact.Service stub.
type covercoreArtifactService struct {
	saved    *artifact.Artifact
	loaded   *artifact.Artifact
	loadErr  error
	keys     []string
	versions []int
	deleted  string
}

func (s *covercoreArtifactService) SaveArtifact(
	_ context.Context, _ artifact.SessionInfo, _ string, a *artifact.Artifact,
) (int, error) {
	s.saved = a
	return 1, nil
}

func (s *covercoreArtifactService) LoadArtifact(
	_ context.Context, _ artifact.SessionInfo, _ string, _ *int,
) (*artifact.Artifact, error) {
	return s.loaded, s.loadErr
}

func (s *covercoreArtifactService) ListArtifactKeys(
	_ context.Context, _ artifact.SessionInfo,
) ([]string, error) {
	return s.keys, nil
}

func (s *covercoreArtifactService) DeleteArtifact(
	_ context.Context, _ artifact.SessionInfo, filename string,
) error {
	s.deleted = filename
	return nil
}

func (s *covercoreArtifactService) ListVersions(
	_ context.Context, _ artifact.SessionInfo, _ string,
) ([]int, error) {
	return s.versions, nil
}

// TestCovercore_ArtifactServiceWrapper covers the wrapper paths the
// existing tests miss.
func TestCovercore_ArtifactServiceWrapper(t *testing.T) {
	require.Nil(t, newArtifactServiceWrapper(nil))

	stub := &covercoreArtifactService{
		loaded:   &artifact.Artifact{MimeType: "text/plain", Data: []byte("clean")},
		keys:     []string{"a.txt"},
		versions: []int{1, 2},
	}
	wrapped := newArtifactServiceWrapper(stub)
	ctx := context.Background()
	info := artifact.SessionInfo{}

	// Passthrough methods.
	keys, err := wrapped.ListArtifactKeys(ctx, info)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, keys)

	require.NoError(t, wrapped.DeleteArtifact(ctx, info, "a.txt"))
	require.Equal(t, "a.txt", stub.deleted)

	versions, err := wrapped.ListVersions(ctx, info, "a.txt")
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, versions)

	// Clean loads pass through.
	loaded, err := wrapped.LoadArtifact(ctx, info, "a.txt", nil)
	require.NoError(t, err)
	require.Equal(t, stub.loaded, loaded)

	// Load errors propagate.
	stub.loadErr = errors.New("storage down")
	_, err = wrapped.LoadArtifact(ctx, info, "a.txt", nil)
	require.ErrorContains(t, err, "storage down")
	stub.loadErr = nil

	// A loaded binary artifact with a secret is refused.
	stub.loaded = &artifact.Artifact{
		MimeType: "application/octet-stream",
		Data:     []byte("AKIAIOSFODNN7EXAMPLE"),
	}
	_, err = wrapped.LoadArtifact(ctx, info, "secret.bin", nil)
	require.Error(t, err)

	// Save errors from redaction propagate; inner never sees the call.
	stub.saved = nil
	_, err = wrapped.SaveArtifact(ctx, info, "secret.bin", &artifact.Artifact{
		MimeType: "application/octet-stream",
		Data:     []byte("AKIAIOSFODNN7EXAMPLE"),
	})
	require.Error(t, err)
	require.Nil(t, stub.saved)
}

// TestCovercore_WrapArtifactService covers the guard-level wrapper switch.
func TestCovercore_WrapArtifactService(t *testing.T) {
	stub := &covercoreArtifactService{}

	// Redaction disabled: the original service is returned unchanged.
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()), WithRedaction(false))
	require.NoError(t, err)
	defer g.Close()
	require.Same(t, stub, g.WrapArtifactService(stub))

	// Redaction enabled: the service is wrapped.
	guard := newTestGuard(t)
	wrapped := guard.WrapArtifactService(stub)
	require.NotSame(t, stub, wrapped)
	_, err = wrapped.SaveArtifact(context.Background(), artifact.SessionInfo{}, "f.txt",
		&artifact.Artifact{MimeType: "text/plain", Data: []byte("AKIAIOSFODNN7EXAMPLE")})
	require.NoError(t, err)
	require.NotContains(t, string(stub.saved.Data), "AKIAIOSFODNN7EXAMPLE")
}

// TestCovercore_GuardScanDirect covers the non-nil Scan passthrough.
func TestCovercore_GuardScanDirect(t *testing.T) {
	guard := newTestGuard(t)
	report, err := guard.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
}

// TestCovercore_NilGuardTrackSession covers the nil-guard early return in
// trackSessionLifecycle.
func TestCovercore_NilGuardTrackSession(t *testing.T) {
	var g *Guard
	require.NotPanics(t, func() {
		g.trackSessionLifecycle("exec_command", map[string]any{"session_id": "s"})
	})
}

// TestCovercore_MaybeAuditPostExecuteNoWriter covers the no-audit-writer
// early return.
func TestCovercore_MaybeAuditPostExecuteNoWriter(t *testing.T) {
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()))
	require.NoError(t, err)
	defer g.Close()
	require.NoError(t, g.maybeAuditPostExecute(ScanEvent{ScanID: "s"}, 0, false, "ok"))
}

// TestCovercore_RedactArtifactBinaryClean covers the binary no-secret
// pass-through returning the original artifact.
func TestCovercore_RedactArtifactBinaryClean(t *testing.T) {
	in := &artifact.Artifact{
		MimeType: "application/octet-stream",
		Name:     "blob.bin",
		Data:     []byte{0x1, 0x2, 0x3},
	}
	out, changed, err := redactArtifact(in)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, in, out)
}
