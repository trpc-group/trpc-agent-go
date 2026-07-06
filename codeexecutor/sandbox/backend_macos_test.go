//go:build darwin

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestMacOSBackendCapabilities(t *testing.T) {
	caps := backendCapabilities(BackendMacOSSandboxExec, WorkspaceWriteProfile())
	if !caps.OSSandbox || !caps.NetworkIsolation || !caps.DenyReadGlob ||
		!caps.ExternalPathGrants || !caps.ProtectedPathMasks {
		t.Fatalf("managed capabilities = %#v, want macOS sandbox features", caps)
	}
	unsupportedCaps := backendCapabilities(BackendLinuxBubblewrap, WorkspaceWriteProfile())
	if unsupportedCaps.OSSandbox || unsupportedCaps.NetworkIsolation ||
		unsupportedCaps.DenyReadGlob || unsupportedCaps.ExternalPathGrants {
		t.Fatalf("unsupported backend capabilities = %#v, want no macOS sandbox features", unsupportedCaps)
	}
	disabledCaps := backendCapabilities(BackendAuto, DangerFullAccessProfile())
	if disabledCaps.OSSandbox || disabledCaps.NetworkIsolation || disabledCaps.ProtectedPathMasks {
		t.Fatalf("disabled capabilities = %#v, want no managed sandbox features", disabledCaps)
	}
}

func TestMacOSSeatbeltProfileGeneration(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "macos/profile", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	externalRead := t.TempDir()
	externalWrite := t.TempDir()
	profile := WorkspaceWriteProfile().
		WithReadPaths(externalRead, "work/read-only").
		WithWritePaths(externalWrite).
		WithNoAccessPaths("work/secret").
		WithNoAccessGlobs("work/*.env").
		WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	policy, err := rt.macosSeatbeltProfile(profile, ws, sandboxDenialRun{})
	if err != nil {
		t.Fatal(err)
	}
	externalReadPolicyPath, err := canonicalizeExistingPath(externalRead)
	if err != nil {
		t.Fatal(err)
	}
	externalWritePolicyPath, err := canonicalizeExistingPath(externalWrite)
	if err != nil {
		t.Fatal(err)
	}
	secretPolicyPath, err := canonicalizeExistingPath(filepath.Join(ws.Path, "work", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(deny default)",
		"(allow file-read* file-map-executable file-test-existence",
		"(allow file-write*",
		"(path-ancestors \"/tmp\")",
		"(path-ancestors \"/var/folders\")",
		sbplString(externalReadPolicyPath),
		sbplString(externalWritePolicyPath),
		"(require-not (literal " + sbplString(secretPolicyPath) + "))",
		`(deny file-read* file-map-executable file-test-existence (regex #"`,
		`(deny file-write* (regex #"`,
		"(allow network-outbound)",
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("macOS policy missing %q:\n%s", want, policy)
		}
	}
	for _, disallow := range []string{
		`(subpath "/tmp")`,
		`(subpath "/private/tmp")`,
		`(subpath "/var/folders")`,
		`(subpath "/private/var/folders")`,
	} {
		if strings.Contains(policy, disallow) {
			t.Fatalf("macOS policy should not grant broad host temp read %q:\n%s", disallow, policy)
		}
	}
}

func TestMacOSPlatformTempMetadataPolicyOnly(t *testing.T) {
	for _, root := range macosPlatformDefaultReadRoots() {
		switch root {
		case "/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp", "/var/folders", "/private/var/folders":
			t.Fatalf("platform default read roots still include host temp path %q", root)
		}
	}
	if !strings.Contains(macosPlatformTempMetadataPolicy, `(path-ancestors "/tmp")`) {
		t.Fatalf("temp metadata policy missing /tmp ancestor metadata")
	}
}

func TestMacOSSandboxDenialParser(t *testing.T) {
	tag := "TRPC_RUN_0123456789abcdef_END_a1b2c3d4e5f67890_SBX"
	line := []byte(`{"timestamp":"2026-07-02 19:36:51.891785+0800","eventMessage":"Sandbox: sh(1503) deny(1) file-read-data /private/tmp/foo\n` + tag + `"}`)
	denial, tagged, ok := parseMacOSSandboxDenialLogLine(line, tag)
	if !ok || !tagged {
		t.Fatalf("parse tagged denial ok=%v tagged=%v denial=%#v", ok, tagged, denial)
	}
	if denial.Operation != "file-read-data" || denial.Target != "/private/tmp/foo" {
		t.Fatalf("denial = %#v, want file-read-data /private/tmp/foo", denial)
	}

	untaggedLine := []byte(`{"timestamp":"2026-07-02 19:36:51.891785+0800","eventMessage":"Sandbox: sh(1503) deny(1) mach-lookup com.apple.trustd.agent"}`)
	untagged, tagged, ok := parseMacOSSandboxDenialLogLine(untaggedLine, tag)
	if !ok || tagged {
		t.Fatalf("parse untagged ok=%v tagged=%v denial=%#v", ok, tagged, untagged)
	}
	if untagged.Operation != "mach-lookup" || untagged.Target != "com.apple.trustd.agent" {
		t.Fatalf("untagged = %#v, want mach-lookup com.apple.trustd.agent", untagged)
	}
}

func TestMacOSDenialDiagnosticsProfileMessages(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "macos/diagnostic-profile", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	tag := "TRPC_RUN_0123456789abcdef_END_a1b2c3d4e5f67890_SBX"
	policy, err := rt.macosSeatbeltProfile(
		WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env"),
		ws,
		sandboxDenialRun{
			enabled:              true,
			runTag:               tag,
			defaultDenyTaggable:  true,
			explicitDenyTaggable: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantDefault := `(deny default (with message "` + tag + `"))`
	if !strings.Contains(policy, wantDefault) {
		t.Fatalf("diagnostic policy missing tagged default deny %q:\n%s", wantDefault, policy)
	}
	wantGlob := `(with message "` + tag + `")`
	if count := strings.Count(policy, wantGlob); count < 3 {
		t.Fatalf("diagnostic policy has %d tagged deny messages, want at least default + glob rules:\n%s", count, policy)
	}

	withoutDefault, err := rt.macosSeatbeltProfile(
		WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env"),
		ws,
		sandboxDenialRun{enabled: true, runTag: tag, explicitDenyTaggable: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withoutDefault, wantDefault) {
		t.Fatalf("diagnostic policy tagged default deny without support:\n%s", withoutDefault)
	}
}

func TestMacOSNetworkExtensionPolicies(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "macos/network-policy", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	restricted, err := rt.macosSeatbeltProfile(WorkspaceWriteProfile(), ws, sandboxDenialRun{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(restricted, "com.apple.trustd.agent") ||
		strings.Contains(restricted, "(allow network-outbound)") {
		t.Fatalf("restricted policy unexpectedly grants broad network/trust services:\n%s", restricted)
	}

	weaker, err := rt.macosSeatbeltProfile(
		WorkspaceWriteProfile().WithMacOSWeakerNetworkIsolation(),
		ws,
		sandboxDenialRun{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(weaker, "com.apple.trustd.agent") {
		t.Fatalf("weaker macOS network policy missing trustd.agent:\n%s", weaker)
	}
	if strings.Contains(weaker, "(allow network-outbound)") {
		t.Fatalf("weaker macOS network policy should not grant broad network:\n%s", weaker)
	}

	socketPath := filepath.Join(t.TempDir(), "demo.sock")
	unixSockets, err := rt.macosSeatbeltProfile(
		WorkspaceWriteProfile().WithMacOSUnixSocketPaths(socketPath),
		ws,
		sandboxDenialRun{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(allow system-socket (socket-domain AF_UNIX))",
		"(allow network-bind (local unix-socket (path-literal " + sbplString(socketPath) + ")))",
		"(allow network-outbound (remote unix-socket (path-literal " + sbplString(socketPath) + ")))",
	} {
		if !strings.Contains(unixSockets, want) {
			t.Fatalf("unix socket policy missing %q:\n%s", want, unixSockets)
		}
	}

	_, err = rt.macosSeatbeltProfile(
		WorkspaceWriteProfile().WithMacOSUnixSocketPaths("relative.sock"),
		ws,
		sandboxDenialRun{},
	)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("relative Unix socket path error = %v, want ErrPolicyViolation", err)
	}
}

func TestMacOSSandboxExecRejectsHostTempFileRead(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	hostTemp := filepath.Join(os.TempDir(), "trpc-agent-sandbox-host-temp-probe")
	if err := os.WriteFile(hostTemp, []byte("host-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(hostTemp) })

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/host-temp", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/cat",
		Args: []string{hostTemp},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("host temp read unexpectedly succeeded: %#v", res)
	}
}

func TestMacOSRuleTargetRejectsAbsoluteWorkspaceSymlinkGrant(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "macos/symlink-grant", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(ws.Path, "work", "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithWritePaths(link)
	_, err = rt.macosSeatbeltProfile(profile, ws, sandboxDenialRun{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("symlink grant profile error = %v, want ErrPathDenied", err)
	}
	target, ok, err := rt.macosRuleTarget(
		profile,
		ws,
		fileSystemRule{Kind: rulePath, Access: accessWrite, Path: link},
	)
	if !isKind(err, ErrPathDenied) || ok || target != "" {
		t.Fatalf("macosRuleTarget = target=%q ok=%v err=%v, want denied", target, ok, err)
	}
}

func TestMacOSRuleTargetRejectsSpecialWorkspaceSymlink(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "macos/special-symlink", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	work := filepath.Join(ws.Path, "work")
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, work); err != nil {
		t.Fatal(err)
	}
	_, err = rt.macosSeatbeltProfile(WorkspaceWriteProfile(), ws, sandboxDenialRun{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("special workspace symlink profile error = %v, want ErrPathDenied", err)
	}
}

func TestMacOSGlobRegexTranslation(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := codeexecutor.Workspace{Path: wsPath}
	regex, ok, err := macosSeatbeltRegexForWorkspaceGlob(ws, "**/*.env")
	if err != nil || !ok {
		t.Fatalf("glob regex err=%v ok=%v", err, ok)
	}
	if !strings.HasPrefix(regex, "^") || !strings.HasSuffix(regex, "$") ||
		!strings.Contains(regex, "(.*/)?") || !strings.Contains(regex, `\.env`) {
		t.Fatalf("glob regex = %q, want anchored doublestar .env regex", regex)
	}
	_, _, err = macosSeatbeltRegexForWorkspaceGlob(ws, "/tmp/*.env")
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("absolute glob error = %v, want ErrPolicyViolation", err)
	}
	_, _, err = macosSeatbeltRegexForWorkspaceGlob(ws, "work/[")
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("invalid glob error = %v, want ErrPolicyViolation", err)
	}
}

func TestMacOSSandboxExecWorkspaceWriteIntegration(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/run", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/sh",
		Args: []string{
			"-c",
			"echo ok > ok.txt; mkdir ../.git 2>&1; echo bad > ../.git/config 2>/dev/null",
		},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("protected metadata write unexpectedly succeeded: %#v", res)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, ".git")); !os.IsNotExist(err) {
		t.Fatalf("protected metadata dir should remain absent: err=%v result=%#v", err, res)
	}
	data, err := os.ReadFile(filepath.Join(ws.Path, "work", "ok.txt"))
	if err != nil {
		t.Fatalf("workspace write missing: %v result=%#v", err, res)
	}
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("workspace write failed: %q", data)
	}
}

func TestMacOSSandboxExecNoAccessGlobIntegration(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")),
	)
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/glob", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "app.env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "cat app.env"},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("glob no-access read unexpectedly succeeded: %#v", res)
	}
}

func TestMain(m *testing.M) {
	resetDiagnosticsCapsCacheForTest()
	os.Exit(m.Run())
}

func TestMacOSSandboxDenialSuffixFormat(t *testing.T) {
	session := newMacOSSessionSuffix()
	if !strings.HasPrefix(session, "_END_") || !strings.HasSuffix(session, "_SBX") {
		t.Fatalf("session suffix = %q, want _END_<hex>_SBX", session)
	}
	probe := newMacOSProbeSuffix()
	if !strings.HasPrefix(probe, "_END_") || !strings.HasSuffix(probe, "_PROBE_SBX") {
		t.Fatalf("probe suffix = %q, want _END_<hex>_PROBE_SBX", probe)
	}
	if strings.HasSuffix(probe, session) {
		t.Fatalf("probe suffix %q must not end with production suffix %q", probe, session)
	}
	tag := newMacOSSandboxDenialRunTag(session)
	if !strings.HasPrefix(tag, "TRPC_RUN_") || !strings.HasSuffix(tag, session) {
		t.Fatalf("run tag = %q, want TRPC_RUN_<hex>%s", tag, session)
	}
}

func TestProbeMatchedRequiresExpectedTarget(t *testing.T) {
	tag := "TRPC_RUN_PROBE_D_0123456789abcdef_END_abc_PROBE_SBX"
	target := "/private/tmp/.trpc_sbx_probe/default_target"
	events := []macosSandboxDenialEvent{{
		denial: Denial{
			Operation: "file-read-data",
			Target:    "/usr/lib/dyld",
			Raw:       "Sandbox: cat deny file-read-data /usr/lib/dyld\n" + tag,
		},
	}}
	if probeMatched(events, probeExpectation{
		Tag:       tag,
		Operation: "file-read-data",
		Target:    target,
	}) {
		t.Fatal("probeMatched accepted dyld startup denial with matching tag")
	}
	events = append(events, macosSandboxDenialEvent{
		denial: Denial{
			Operation: "file-read-data",
			Target:    target,
			Raw:       "Sandbox: cat deny file-read-data " + target + "\n" + tag,
		},
	})
	if !probeMatched(events, probeExpectation{
		Tag:       tag,
		Operation: "file-read*",
		Target:    target,
	}) {
		t.Fatal("probeMatched rejected intentional probe denial")
	}
}

func TestProbeMatchedRequiresExactTag(t *testing.T) {
	tag := "TRPC_RUN_PROBE_D_0123456789abcdef_END_abc_PROBE_SBX"
	target := "/private/tmp/.trpc_sbx_probe/default_target"
	events := []macosSandboxDenialEvent{{
		denial: Denial{
			Operation: "file-read-data",
			Target:    target,
			Raw:       "Sandbox: cat deny file-read-data " + target + "\n" + tag + "_EXTRA",
		},
	}}
	if probeMatched(events, probeExpectation{
		Tag:       tag,
		Operation: "file-read*",
		Target:    target,
	}) {
		t.Fatal("probeMatched accepted a tag that only matched as a prefix")
	}
}

func TestProbeMatchedAcceptsReadOperationFamily(t *testing.T) {
	tag := "TRPC_RUN_PROBE_D_0123456789abcdef_END_abc_PROBE_SBX"
	target := "/private/tmp/.trpc_sbx_probe/default_target"
	for _, operation := range []string{
		"file-read-data",
		"file-read-metadata",
		"file-test-existence",
		"file-map-executable",
	} {
		events := []macosSandboxDenialEvent{{
			denial: Denial{
				Operation: operation,
				Target:    target,
				Raw:       "Sandbox: cat deny " + operation + " " + target + "\n" + tag,
			},
		}}
		if !probeMatched(events, probeExpectation{
			Tag:       tag,
			Operation: "file-read*",
			Target:    target,
		}) {
			t.Fatalf("probeMatched rejected operation %q", operation)
		}
	}
}

func TestMacOSDiagnosticsProbePolicyUsesProductionLikeExplicitDeny(t *testing.T) {
	policy := macosDiagnosticsProbePolicy(
		"TRPC_RUN_PROBE_D_0123456789abcdef_END_abc_PROBE_SBX",
		"TRPC_RUN_PROBE_E_0123456789abcdef_END_abc_PROBE_SBX",
		"/private/tmp/.trpc_sbx_probe/explicit_target",
	)
	if !strings.Contains(policy, `(deny file-read* file-map-executable file-test-existence (regex #"`) {
		t.Fatalf("probe policy does not use production-like explicit read deny:\n%s", policy)
	}
	if strings.Contains(policy, `(deny file-read-data (literal`) {
		t.Fatalf("probe policy still uses literal file-read-data explicit deny:\n%s", policy)
	}
}

func TestCollectSandboxDenialsWaitsForCurrentRunTag(t *testing.T) {
	runTag := "TRPC_RUN_current_END_0123456789abcdef_SBX"
	ring := &macosDenialRing{
		events: []macosSandboxDenialEvent{{
			denial: Denial{
				Operation: "file-read-data",
				Target:    "/private/tmp/old",
				Raw:       "Sandbox: cat deny(1) file-read-data /private/tmp/old\nTRPC_RUN_old_END_0123456789abcdef_SBX",
			},
			tagged: true,
		}},
	}
	rt := NewRuntime()
	rt.macosDenialDiagnostics().prodMonitor = &macosLogStreamMonitor{ring: ring}
	go func() {
		time.Sleep(120 * time.Millisecond)
		ring.mu.Lock()
		defer ring.mu.Unlock()
		ring.events = append(ring.events, macosSandboxDenialEvent{
			denial: Denial{
				Operation: "file-read-data",
				Target:    "/private/tmp/current",
				Raw:       "Sandbox: cat deny(1) file-read-data /private/tmp/current\n" + runTag,
			},
			tagged: true,
		})
	}()
	denials := rt.collectSandboxDenials(runTag, "/bin/cat", 2*time.Second)
	if len(denials) != 1 || denials[0].Target != "/private/tmp/current" {
		t.Fatalf("denials=%#v, want current run denial after stale ring event", denials)
	}
}

func TestRunProgramWithDiagnosticsDegradesWhenMonitorUnavailable(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/diagnostics-degraded", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ensureDenialMonitor(); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = nil
	d.caps = DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: false,
		StrongCorrelation:    false,
	}
	d.mu.Unlock()

	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "echo ok"},
	})
	diagnostics := <-diagnosticsCh
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "ok" {
		t.Fatalf("run result = %#v, want successful echo ok", res)
	}
	if diagnostics.Denials != nil {
		t.Fatalf("diagnostics = %#v, want nil denials when monitor unavailable", diagnostics)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatalf("sandboxDenialCollectingReady = true, want false after simulated monitor loss")
	}
}

func TestDiagnosticsCapabilityProbe(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	if err := rt.ensureDenialMonitor(); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	caps := rt.DiagnosticsCapability()
	t.Logf("caps=%+v", caps)
	if !caps.ProbeCompleted {
		t.Skip("diagnostics capability probe did not complete on this host")
	}
	if !caps.EventStreamAvailable {
		t.Skip("log stream unavailable on this host")
	}
	if !caps.DefaultDenyTaggable || !caps.ExplicitDenyTaggable {
		t.Fatalf("expected both default and explicit deny tagging on this host, caps=%+v", caps)
	}
}

func TestMacOSSandboxExecCollectsExplicitDenyDiagnostics(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")),
	)
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/glob-denial-diagnostics", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "app.env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "cat app.env"},
	})
	diagnostics := <-diagnosticsCh
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("glob no-access read unexpectedly succeeded: %#v", res)
	}
	if len(diagnostics.Denials) == 0 {
		t.Fatalf("sandbox denials empty, result=%#v", res)
	}
	if !strings.Contains(diagnostics.Denials[0].Operation, "file-read") {
		t.Fatalf("sandbox denial = %#v, want file-read operation", diagnostics.Denials[0])
	}
	if strings.Contains(res.Stderr, "[sandbox diagnostics]") {
		t.Fatalf("stderr contains framework sandbox diagnostics: %q", res.Stderr)
	}
}

func TestMacOSSandboxExecCollectsDefaultDenyDiagnosticsWhenSupported(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	_ = rt.ensureDenialMonitor()
	caps := rt.DiagnosticsCapability()
	if !caps.DefaultDenyTaggable {
		t.Skip("default-deny messages are not supported on this host")
	}
	hostTemp := filepath.Join(os.TempDir(), "trpc-agent-sandbox-default-deny-diagnostics")
	if err := os.WriteFile(hostTemp, []byte("host-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(hostTemp) })
	ws, err := rt.CreateWorkspace(context.Background(), "macos/default-deny-diagnostics", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/cat",
		Args: []string{hostTemp},
	})
	diagnostics := <-diagnosticsCh
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("host temp read unexpectedly succeeded: %#v", res)
	}
	if len(diagnostics.Denials) == 0 {
		t.Fatalf("default deny diagnostics empty, result=%#v", res)
	}
	if strings.Contains(res.Stderr, "[sandbox diagnostics]") {
		t.Fatalf("stderr contains framework sandbox diagnostics: %q", res.Stderr)
	}
}

func TestMacOSSandboxExecNoAccessGlobHardDenyOverridesSpecificRead(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	profile := ReadOnlyProfile().
		WithReadPaths("work/public/secret.txt").
		WithNoAccessGlobs("work/**")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/glob-hard-deny", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(ws.Path, "work", "public", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(secret), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("visible to Go layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(context.Background(), ws, []string{"work/public/secret.txt"})
	if err != nil || len(files) != 1 {
		t.Fatalf("Go-layer Collect = files:%d err:%v, want specific read grant to win", len(files), err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "cat work/public/secret.txt"},
		Cwd:  ".",
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("glob hard deny was reopened by specific read grant: %#v", res)
	}
}

func TestMacOSSandboxExecChildProcessInheritsSandbox(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	hostTemp := filepath.Join(os.TempDir(), "trpc-agent-sandbox-child-inherit-probe")
	if err := os.WriteFile(hostTemp, []byte("host-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(hostTemp) })

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/child-inherit", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/sh",
		Args: []string{
			"-c",
			`(cat "$1" > child.out 2>/dev/null; echo $? > child.status) & wait; cat child.status`,
			"sh",
			hostTemp,
		},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	statusText := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || statusText == "" {
		t.Fatalf("child status probe did not complete: %#v", res)
	}
	status, parseErr := strconv.Atoi(statusText)
	if parseErr != nil {
		t.Fatalf("child status = %q: %v", statusText, parseErr)
	}
	if status == 0 {
		t.Fatalf("child process escaped sandbox and read host temp: %#v", res)
	}
}

func TestMacOSSandboxExecAllowsConfiguredUnixSocket(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	socketDir, err := os.MkdirTemp("/tmp", "trpc-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "demo.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	socketPolicyPath, err := canonicalizeExistingPath(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if unixListener, ok := listener.(*net.UnixListener); ok {
		if err := unixListener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = conn.Write([]byte("UNIX_SOCKET_OK\n"))
		done <- err
	}()

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(
			WorkspaceWriteProfile().
				WithReadPaths(os.Args[0]).
				WithMacOSUnixSocketPaths(socketPath),
		),
	)
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/unix-socket", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     os.Args[0],
		Args:    []string{"-test.run=TestMacOSUnixSocketClientHelper"},
		Env:     map[string]string{"TRPC_MACOS_UNIX_SOCKET_HELPER": "1", "TRPC_MACOS_UNIX_SOCKET_PATH": socketPolicyPath},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "UNIX_SOCKET_OK") {
		t.Fatalf("unix socket run = %#v, want successful socket read", res)
	}
	if err := <-done; err != nil {
		t.Fatalf("unix socket server error: %v", err)
	}
}

func TestMacOSUnixSocketClientHelper(t *testing.T) {
	if os.Getenv("TRPC_MACOS_UNIX_SOCKET_HELPER") != "1" {
		return
	}
	conn, err := net.DialTimeout("unix", os.Getenv("TRPC_MACOS_UNIX_SOCKET_PATH"), time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data, err := io.ReadAll(conn)
	_ = conn.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	fmt.Print(string(data))
	os.Exit(0)
}

func TestMacOSSandboxExecTimeoutKillsProcessGroup(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/process-timeout", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "/bin/sh",
		Args:    []string{"-c", "sleep 30 & echo $! > child.pid; wait"},
		Timeout: 2 * time.Second,
	})
	if !isKind(err, ErrTimeout) || !res.TimedOut {
		t.Fatalf("timeout run = result:%#v err:%v, want ErrTimeout", res, err)
	}
	pidPath := filepath.Join(ws.Path, "work", "child.pid")
	var pidBytes []byte
	var readErr error
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pidBytes, readErr = os.ReadFile(pidPath)
		if readErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if readErr != nil {
		t.Fatalf("child pid file missing after timeout: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if parseErr != nil {
		t.Fatalf("child pid = %q: %v", pidBytes, parseErr)
	}
	cleanupPID := 0
	t.Cleanup(func() {
		if cleanupPID == 0 {
			return
		}
		_ = syscall.Kill(cleanupPID, syscall.SIGKILL)
	})
	for i := 0; i < 20; i++ {
		if !processExists(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	cleanupPID = pid
	t.Fatalf("background child process %d still exists after timeout cleanup", pid)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
