//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeExecutor struct {
	engine   codeexecutor.Engine
	closeErr error
}

func (f fakeExecutor) Engine() codeexecutor.Engine { return f.engine }
func (f fakeExecutor) Close() error                { return f.closeErr }

type fakeRuntime struct {
	createErr, cleanupErr, stageErr, runErr, collectErr error
	runResult                                           codeexecutor.RunResult
	manifest                                            codeexecutor.OutputManifest
	cleanupHadDeadline                                  bool
	stageCalls, runCalls                                int
	stageSource, stageDestination                       string
	stageOptions                                        codeexecutor.StageOptions
}

func (f *fakeRuntime) CreateWorkspace(_ context.Context, execID string, _ codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: execID, Path: "/tmp/run/ws_" + execID + "_1"}, f.createErr
}
func (f *fakeRuntime) Cleanup(ctx context.Context, _ codeexecutor.Workspace) error {
	_, f.cleanupHadDeadline = ctx.Deadline()
	return f.cleanupErr
}
func (f *fakeRuntime) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}

func (f *fakeRuntime) StageDirectory(_ context.Context, _ codeexecutor.Workspace, source, destination string, options codeexecutor.StageOptions) error {
	f.stageCalls++
	f.stageSource, f.stageDestination, f.stageOptions = source, destination, options
	return f.stageErr
}
func (f *fakeRuntime) Collect(context.Context, codeexecutor.Workspace, []string) ( //nolint:revive // Signature is fixed by codeexecutor.FileSystem.
	[]codeexecutor.File, error) {
	return nil, nil
}
func (f *fakeRuntime) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}
func (f *fakeRuntime) CollectOutputs(_ context.Context, _ codeexecutor.Workspace, spec codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	if f.collectErr != nil {
		return codeexecutor.OutputManifest{}, f.collectErr
	}
	if f.manifest.Files == nil {
		name := strings.TrimPrefix(spec.Globs[0], "out/")
		content := `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""}`
		return codeexecutor.OutputManifest{Files: []codeexecutor.FileRef{{Name: "out/" + name, Content: content, SizeBytes: int64(len(content))}}, LimitsHit: f.manifest.LimitsHit}, nil
	}
	manifest := f.manifest
	for index := range manifest.Files {
		if manifest.Files[index].Name == "" {
			manifest.Files[index].Name = spec.Globs[0]
		}
		if manifest.Files[index].SizeBytes == 0 {
			manifest.Files[index].SizeBytes = int64(len(manifest.Files[index].Content))
		}
	}
	return manifest, nil
}
func (f *fakeRuntime) RunProgram(_ context.Context, _ codeexecutor.Workspace, spec codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	f.runCalls++
	if spec.Cmd != "/usr/bin/head" {
		return f.runResult, f.runErr
	}
	result := f.runResult
	result.Stderr, result.TimedOut, result.ExitCode = "", false, 0
	if result.Stdout == "" {
		result.Stdout = `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""}`
		if len(f.manifest.Files) > 0 {
			result.Stdout = f.manifest.Files[0].Content
		}
	}
	return result, nil
}

type testRecorder struct {
	values []governance.Decision
}

func (r *testRecorder) SaveDecision(_ context.Context, value governance.Decision) error {
	r.values = append(r.values, value)
	return nil
}
func TestContainerCheckWithFakeEngine(t *testing.T) {
	runtime := &fakeRuntime{}
	recorder := &testRecorder{}
	container := testContainer(t, runtime, recorder)
	repo := t.TempDir()
	result, err := container.Check(context.Background(), "go-test", repo, time.Second)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Status != "completed" || result.ExitCode != 0 || result.SHA256 == "" || result.ArtifactBytes == 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(recorder.values) != 2 {
		t.Fatalf("decisions = %d", len(recorder.values))
	}
	if !runtime.cleanupHadDeadline {
		t.Fatal("workspace cleanup has no deadline")
	}
	if runtime.stageCalls != 1 || runtime.stageSource != repo || runtime.stageDestination != codeexecutor.DirWork || !runtime.stageOptions.ReadOnly || !runtime.stageOptions.AllowMount {
		t.Fatalf("staging calls=%d source=%q destination=%q options=%+v", runtime.stageCalls, runtime.stageSource, runtime.stageDestination, runtime.stageOptions)
	}
}
func TestContainerCheckFailures(t *testing.T) {
	tests := []struct {
		name, want, content string
		runtime             fakeRuntime
		nilEngine           bool
		closeErr            error
	}{
		{name: "nil engine", want: "does not support clean environment", nilEngine: true},
		{name: "create", want: "create failed", runtime: fakeRuntime{createErr: errors.New("create failed")}},
		{name: "run", want: "run failed", runtime: fakeRuntime{runErr: errors.New("run failed")}},
		{name: "stage", want: "stage failed", runtime: fakeRuntime{stageErr: errors.New("stage failed")}},
		{name: "missing result", want: "decode runner result", content: " "},
		{name: "close", want: "close failed", closeErr: errors.New("close failed")},
		{name: "bad json", want: "decode runner result", content: "{"},
		{name: "unknown json field", want: "unknown field", content: `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":"","extra":true}`},
		{name: "missing json field", want: "missing field", content: `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false}`},
		{name: "trailing json", want: "trailing JSON", content: `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""} {}`},
		{name: "duration", want: "duration is outside", content: `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":999999,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""}`},
		{name: "output bound", want: "output exceeds", content: `{"check_id":"go-test","exit_code":1,"timed_out":false,"duration_ms":1,"stdout":"` + strings.Repeat("x", runnerOutputBytes+1) + `","stderr":"","stdout_truncated":true,"stderr_truncated":false,"error":""}`},
		{name: "timeout success", want: "success exit code", content: `{"check_id":"go-test","exit_code":0,"timed_out":true,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""}`},
		{name: "wrong check", want: "check ID mismatch", content: `{"check_id":"go-vet","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &test.runtime
			if test.content != "" {
				runtime.manifest.Files = []codeexecutor.FileRef{{Content: test.content}}
			}
			executor := &fakeExecutor{engine: codeexecutor.NewEngineWithCapabilities(runtime, runtime, runtime, codeexecutor.Capabilities{SupportsCleanEnv: true})}
			if test.nilEngine {
				executor.engine = nil
			}
			executor.closeErr = test.closeErr
			container := testContainer(t, runtime, &testRecorder{})
			container.factory = func(string) (containerExecutor, error) {
				return executor, nil
			}
			result, err := container.Check(context.Background(), "go-test", t.TempDir(), time.Second)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Check() error = %v, want %q", err, test.want)
			}
			if test.name == "close" && (!errors.Is(err, ErrLifecycle) || result.Status != "failed") {
				t.Fatalf("lifecycle result=%#v error=%v", result, err)
			}
		})
	}
}

func TestContainerRunnerStatusesFromArtifact(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantStatus string
		wantExit   int
		truncated  bool
	}{
		{"completed", `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"ok","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":""}`, "completed", 0, false},
		{"failed", `{"check_id":"go-test","exit_code":2,"timed_out":false,"duration_ms":1,"stdout":"","stderr":"failed","stdout_truncated":false,"stderr_truncated":false,"error":"exit status 2"}`, "failed", 2, false},
		{"timeout", `{"check_id":"go-test","exit_code":-1,"timed_out":true,"duration_ms":1,"stdout":"","stderr":"","stdout_truncated":false,"stderr_truncated":false,"error":"deadline exceeded"}`, "timeout", -1, false},
		{"truncated", `{"check_id":"go-test","exit_code":0,"timed_out":false,"duration_ms":1,"stdout":"bounded","stderr":"","stdout_truncated":true,"stderr_truncated":false,"error":""}`, "completed", 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &fakeRuntime{manifest: codeexecutor.OutputManifest{Files: []codeexecutor.FileRef{{Content: test.content}}}}
			container := testContainer(t, runtime, &testRecorder{})
			result, err := container.Check(context.Background(), "go-test", t.TempDir(), time.Second)
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if result.Status != test.wantStatus || result.ExitCode != test.wantExit || result.Truncated != test.truncated || result.ArtifactBytes == 0 || result.SHA256 == "" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestContainerTerminalAndCleanupErrors(t *testing.T) {
	runtime := &fakeRuntime{runResult: codeexecutor.RunResult{Stderr: "unexpected"}, cleanupErr: errors.New("cleanup failed")}
	container := testContainer(t, runtime, &testRecorder{})
	result, err := container.Check(context.Background(), "go-test", t.TempDir(), time.Second)
	if err == nil || !errors.Is(err, ErrLifecycle) || result.Status != "failed" ||
		!strings.Contains(err.Error(), "trusted runner emitted stderr") || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("Check() result=%#v error=%v", result, err)
	}
}
func TestContainerCheckFactoryFailure(t *testing.T) {
	container := testContainer(t, &fakeRuntime{}, &testRecorder{})
	container.factory = func(string) (containerExecutor, error) {
		return nil, errors.New("factory failed")
	}
	if _, err := container.Check(context.Background(), "go-test", t.TempDir(), time.Second); err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("Check() error = %v", err)
	}
}
func TestContainerCheckDenyStopsBeforeStaging(t *testing.T) {
	runtime := &fakeRuntime{}
	container := testContainer(t, runtime, &testRecorder{})
	container.Authorizer.Policy = tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return tool.DenyPermission("blocked"), nil
	})
	if _, err := container.Check(context.Background(), "go-test", t.TempDir(), time.Second); err == nil {
		t.Fatal("denied check returned nil error")
	}
	if runtime.stageCalls != 0 || runtime.runCalls != 0 || !runtime.cleanupHadDeadline {
		t.Fatalf("stage calls=%d run calls=%d cleanup deadline=%t", runtime.stageCalls, runtime.runCalls, runtime.cleanupHadDeadline)
	}
}
func testContainer(t *testing.T, fake *fakeRuntime, recorder *testRecorder) Container {
	t.Helper()
	root := exampleRoot(t)
	engine := codeexecutor.NewEngineWithCapabilities(fake, fake, fake, codeexecutor.Capabilities{SupportsCleanEnv: true})
	return Container{Authorizer: governance.Authorizer{Policy: tool.PermissionPolicyFunc(governance.DefaultPolicy), Recorder: recorder}, BuildContext: root, SkillRoot: filepath.Join(root, "skills", "code-review"), factory: func(string) (containerExecutor, error) {
		return fakeExecutor{engine: engine}, nil
	}}
}
func TestFakeCheckPreservesGovernanceWithoutExecution(t *testing.T) {
	recorder := &testRecorder{}
	checker := Fake{Authorizer: governance.Authorizer{Policy: tool.PermissionPolicyFunc(governance.DefaultPolicy), Recorder: recorder}, SkillRoot: filepath.Join(exampleRoot(t), "skills", "code-review")}
	result, err := checker.Check(context.Background(), "go-test", t.TempDir(), time.Second)
	if err != nil {
		t.Fatalf("Fake.Check() error = %v", err)
	}
	if result.Runtime != "fake" || result.Status != "completed" || result.ExitCode != 0 || result.Artifact == "" || result.SHA256 == "" || len(recorder.values) != 2 {
		t.Fatalf("result = %#v, decisions = %#v", result, recorder.values)
	}
}

func TestFakeCheckFailureBoundaries(t *testing.T) {
	skillRoot := filepath.Join(exampleRoot(t), "skills", "code-review")
	denied := Fake{Authorizer: governance.Authorizer{Policy: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return tool.DenyPermission("blocked"), nil
	}), Recorder: &testRecorder{}}, SkillRoot: skillRoot}
	if _, err := denied.Check(context.Background(), "go-test", t.TempDir(), time.Second); err == nil {
		t.Fatal("denied fake check executed")
	}
}
func TestLocalCheckRunsFixedOfflineCommand(t *testing.T) {
	repo := t.TempDir()
	writeLocalFixture(t, repo, "go.mod", "module example.test/local\n\ngo 1.23\n")
	writeLocalFixture(t, repo, "local.go", "package local\n\nfunc Value() int { return 1 }\n")
	recorder := &testRecorder{}
	checker := Local{Authorizer: governance.Authorizer{Policy: tool.PermissionPolicyFunc(governance.DefaultPolicy), Recorder: recorder}, SkillRoot: filepath.Join(exampleRoot(t), "skills", "code-review"), WorkRoot: t.TempDir()}
	result, err := checker.Check(context.Background(), "go-test", repo, 30*time.Second)
	if err != nil {
		t.Fatalf("Local.Check() error = %v", err)
	}
	if result.Runtime != "local" || result.Status != "completed" || result.ExitCode != 0 || len(recorder.values) != 2 {
		t.Fatalf("result = %#v, decisions = %#v", result, recorder.values)
	}
	if recorder.values[0].Risk != "high" {
		t.Fatalf("risk = %q", recorder.values[0].Risk)
	}
}

func TestLocalHelpersAreBounded(t *testing.T) {
	checker := Local{WorkRoot: t.TempDir()}
	workRoot, err := checker.createWorkRoot(t.TempDir())
	if err != nil {
		t.Fatalf("createWorkRoot() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workRoot) })
	for _, name := range []string{"home", "gocache", "gomodcache", "tmp"} {
		if info, err := os.Stat(filepath.Join(workRoot, name)); err != nil || !info.IsDir() {
			t.Fatalf("cache directory %q: info=%v error=%v", name, info, err)
		}
	}
	environment := localEnvironment(workRoot, t.TempDir())
	listed := strings.Join(environmentList(environment), "\n")
	if !strings.Contains(listed, "GOPROXY=off") || !strings.Contains(listed, "GOENV=off") || strings.Contains(listed, "UNTRUSTED=") {
		t.Fatalf("environment = %q", listed)
	}
	buffer := &limitedBuffer{limit: 3}
	if n, err := buffer.Write([]byte("ab")); err != nil || n != 2 || buffer.truncated {
		t.Fatalf("first Write() = %d, %v, truncated=%v", n, err, buffer.truncated)
	}
	if n, err := buffer.Write([]byte("cdef")); err != nil || n != 4 || buffer.String() != "abc" || !buffer.truncated {
		t.Fatalf("second Write() = %d, %v, content=%q truncated=%v", n, err, buffer.String(), buffer.truncated)
	}
	if processExitCode(&exec.Cmd{}) != -1 {
		t.Fatal("nil process state returned a success exit code")
	}
	blockedRoot := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blockedRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Local{WorkRoot: blockedRoot}).createWorkRoot(t.TempDir()); err == nil {
		t.Fatal("file accepted as local work root")
	}
}
func writeLocalFixture(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %q: %v", name, err)
	}
}

const inspectTimeout = 45 * time.Second

type integrationRecorder struct{ count int }

func (r *integrationRecorder) SaveDecision(context.Context, governance.Decision) error {
	r.count++
	return nil
}

func TestContainerCheckRealDocker(t *testing.T) {
	if os.Getenv("CODE_REVIEW_DOCKER_TEST") != "1" {
		t.Skip("set CODE_REVIEW_DOCKER_TEST=1 for real Docker smoke")
	}
	root := exampleRoot(t)
	recorder := &integrationRecorder{}
	runner := Container{
		Authorizer:   governance.Authorizer{Policy: tool.PermissionPolicyFunc(governance.DefaultPolicy), Recorder: recorder},
		BuildContext: root, SkillRoot: filepath.Join(root, "skills", "code-review"),
	}
	result, err := runner.Check(context.Background(), "go-test", filepath.Join(root, "fixtures", "composite", "repo"), 30*time.Second)
	if err != nil || result.Status != "completed" || result.ExitCode != 0 || result.ArtifactBytes <= 0 || result.SHA256 == "" || recorder.count != 2 {
		t.Fatalf("Check() result=%#v decisions=%d error=%v", result, recorder.count, err)
	}
}

func TestContainerTimeoutAndOutputLimitRealDocker(t *testing.T) {
	if os.Getenv("CODE_REVIEW_DOCKER_TEST") != "1" {
		t.Skip("set CODE_REVIEW_DOCKER_TEST=1 for real Docker smoke")
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module sandboxlimits\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtimeout := `package sandboxlimits
import ("os"; "os/exec"; "testing")
func TestMain(m *testing.M) {
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	_ = cmd.Run()
	os.Exit(m.Run())
}`
	if err := os.WriteFile(filepath.Join(repo, "limits_test.go"), []byte(mtimeout), 0o600); err != nil {
		t.Fatal(err)
	}
	root := exampleRoot(t)
	runner := Container{Authorizer: governance.Authorizer{Policy: tool.PermissionPolicyFunc(governance.DefaultPolicy), Recorder: &integrationRecorder{}}, BuildContext: root, SkillRoot: filepath.Join(root, "skills", "code-review")}
	result, err := runner.Check(context.Background(), "go-test", repo, 30*time.Second)
	if err != nil || result.Status != "timeout" || !result.TimedOut {
		t.Fatalf("Check() result=%#v error=%v", result, err)
	}
	outputRepo := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputRepo, "go.mod"), []byte("module sandboxoutput\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := `package sandboxoutput
import ("fmt"; "strings"; "testing")
func TestOutput(t *testing.T) { fmt.Print(strings.Repeat("x", 70000)); t.Fatal("failed") }`
	if err := os.WriteFile(filepath.Join(outputRepo, "output_test.go"), []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	outputResult, outputErr := runner.Check(context.Background(), "go-test", outputRepo, 30*time.Second)
	if outputErr != nil || outputResult.Status != "failed" || !outputResult.Truncated || len(outputResult.Stdout) > runnerOutputBytes {
		t.Fatalf("output Check() result=%#v error=%v", outputResult, outputErr)
	}
	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, name := range []string{result.ContainerName, outputResult.ContainerName} {
		if _, err := client.ContainerInspect(context.Background(), name); !errdefs.IsNotFound(err) {
			t.Fatalf("container %q remains: %v", name, err)
		}
	}
}

func TestContainerConfigurationRealDocker(t *testing.T) {
	if os.Getenv("CODE_REVIEW_DOCKER_TEST") != "1" {
		t.Skip("set CODE_REVIEW_DOCKER_TEST=1 for real Docker smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer cancel()
	root := exampleRoot(t)
	name := "cr-inspect-" + mustRandomToken(t)
	runner := Container{BuildContext: root, SkillRoot: filepath.Join(root, "skills", "code-review")}
	repo := filepath.Join(root, "fixtures", "composite", "repo")
	executor, err := runner.newExecutor(ctx, repo, name)
	if err != nil {
		t.Fatalf("newExecutor() error = %v", err)
	}
	t.Cleanup(func() {
		if err := executor.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("NewClientWithOpts() error = %v", err)
	}
	t.Cleanup(func() {
		if err := dockerClient.Close(); err != nil {
			t.Errorf("docker client Close() error = %v", err)
		}
	})
	inspected, err := dockerClient.ContainerInspect(ctx, name)
	if err != nil {
		t.Fatalf("ContainerInspect() error = %v", err)
	}
	assertContainerConfig(t, inspected.Config, inspected.HostConfig)
	if err := executor.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := dockerClient.ContainerInspect(ctx, name); !errdefs.IsNotFound(err) {
		t.Fatalf("container remains after close: %v", err)
	}
}

func assertContainerConfig(t *testing.T, config *container.Config, host *container.HostConfig) {
	t.Helper()
	if config.Image != ImageTag || config.User != "0:0" || !slices.Equal([]string(config.Cmd), []string{"tail", "-f", "/dev/null"}) {
		t.Fatalf("container config = %#v", config)
	}
	if host.AutoRemove || host.Privileged || !host.ReadonlyRootfs || host.NetworkMode != "none" {
		t.Fatalf("host hardening = %#v", host)
	}
	if len(host.Binds) != 1 || !strings.HasSuffix(host.Binds[0], ":"+stagingSourcePath+":ro") {
		t.Fatalf("staging source is not read-only = %v", host.Binds)
	}
	for _, capability := range []string{"SETUID", "SETGID", "KILL"} {
		if !hasCapability([]string(host.CapAdd), capability) {
			t.Fatalf("missing capability %s: %v", capability, host.CapAdd)
		}
	}
	if !slices.Contains([]string(host.CapDrop), "ALL") || !slices.Contains(host.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("capabilities/security = %v %v", host.CapDrop, host.SecurityOpt)
	}
	if host.Resources.Memory <= 0 || host.Resources.NanoCPUs <= 0 || host.Resources.PidsLimit == nil {
		t.Fatalf("resource limits = %#v", host.Resources)
	}
}

func hasCapability(values []string, capability string) bool {
	return slices.Contains(values, capability) || slices.Contains(values, "CAP_"+capability)
}

func mustRandomToken(t *testing.T) string {
	t.Helper()
	value, err := randomToken()
	if err != nil {
		t.Fatalf("randomToken() error = %v", err)
	}
	return value
}

func exampleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
