//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestGovernanceCreatesAndClosesIsolatedSandboxPerCommand(t *testing.T) {
	var runners []*isolatedSandboxTestRunner
	result, err := runGovernance(
		context.Background(),
		config{enableStaticcheck: true, effectiveRuntime: runtimeLocal},
		reviewInput{
			kind:                     inputKindRepoPath,
			repoRoot:                 t.TempDir(),
			sandboxRepoRoot:          t.TempDir(),
			sandboxDiagnosticModules: testRootDiagnosticModules(),
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunnerFactory: func(context.Context) (sandboxRunner, error) {
			runner := &isolatedSandboxTestRunner{id: len(runners)}
			if runner.id == 2 {
				runner.closeErr = errors.New("cleanup failed")
			}
			runners = append(runners, runner)
			return runner, nil
		}},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if len(runners) != 4 || result.ToolCalls != 4 || len(result.SandboxRuns) != 4 {
		t.Fatalf("runners = %d, tool calls = %d, runs = %d", len(runners), result.ToolCalls, len(result.SandboxRuns))
	}
	for i, runner := range runners {
		if runner.calls != 1 || runner.closes != 1 {
			t.Fatalf("runner %d calls = %d, closes = %d", i, runner.calls, runner.closes)
		}
	}
	wantInputs := runners[1].spec.Inputs
	for i := 2; i < len(runners); i++ {
		if !reflect.DeepEqual(runners[i].spec.Inputs, wantInputs) {
			t.Fatalf("runner %d inputs = %+v, want %+v", i, runners[i].spec.Inputs, wantInputs)
		}
	}
	if !strings.Contains(strings.Join(result.SandboxRuns[2].Warnings, "\n"), "cleanup failed") {
		t.Fatalf("cleanup warning missing from run: %+v", result.SandboxRuns[2])
	}
	if finalized := finalizeRuleMatches(result.Matches); !finalized.NeedsHumanReview {
		t.Fatalf("cleanup failure did not require human review: %+v", finalized)
	}
	if result.SandboxRuns[3].Command != string(commandCheckStaticcheck) || result.SandboxRuns[3].Skipped {
		t.Fatalf("later command did not run independently: %+v", result.SandboxRuns[3])
	}
}

func TestGovernanceIsolatedSandboxStagingFailureOnlyAffectsCurrentCommand(t *testing.T) {
	var runners []*isolatedSandboxTestRunner
	result, err := runGovernance(
		context.Background(),
		config{enableStaticcheck: true, effectiveRuntime: runtimeE2B},
		reviewInput{
			kind:                     inputKindRepoPath,
			repoRoot:                 t.TempDir(),
			sandboxRepoRoot:          t.TempDir(),
			sandboxDiagnosticModules: testRootDiagnosticModules(),
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunnerFactory: func(context.Context) (sandboxRunner, error) {
			runner := &isolatedSandboxTestRunner{id: len(runners)}
			if runner.id == 1 {
				runner.runErr = errors.New("stage inputs: copy failed")
			}
			runners = append(runners, runner)
			return runner, nil
		}},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if len(runners) != 4 || result.ToolCalls != 4 || len(result.SandboxRuns) != 4 {
		t.Fatalf("runners = %d, tool calls = %d, runs = %d", len(runners), result.ToolCalls, len(result.SandboxRuns))
	}
	if got := result.SandboxRuns[1]; !strings.Contains(got.Error, "stage inputs: copy failed") {
		t.Fatalf("go test run = %+v, want staging failure", got)
	}
	for _, index := range []int{2, 3} {
		if got := result.SandboxRuns[index]; got.Skipped || sandboxRunFailed(got) {
			t.Fatalf("later run %d = %+v, want independent success", index, got)
		}
	}
	for i, runner := range runners {
		if runner.calls != 1 || runner.closes != 1 {
			t.Fatalf("runner %d calls = %d, closes = %d", i, runner.calls, runner.closes)
		}
	}
}

func TestGovernanceInvokedStaticcheckSkipCountsToolCall(t *testing.T) {
	var runners []*isolatedSandboxTestRunner
	result, err := runGovernance(
		context.Background(),
		config{enableStaticcheck: true, effectiveRuntime: runtimeLocal},
		reviewInput{
			kind:                     inputKindRepoPath,
			repoRoot:                 t.TempDir(),
			sandboxRepoRoot:          t.TempDir(),
			sandboxDiagnosticModules: testRootDiagnosticModules(),
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunnerFactory: func(context.Context) (sandboxRunner, error) {
			runner := &isolatedSandboxTestRunner{id: len(runners)}
			if runner.id == 3 {
				runner.skipped = true
			}
			runners = append(runners, runner)
			return runner, nil
		}},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if len(runners) != 4 || result.ToolCalls != 4 || len(result.SandboxRuns) != 4 {
		t.Fatalf("runners = %d, tool calls = %d, runs = %d", len(runners), result.ToolCalls, len(result.SandboxRuns))
	}
	if got := result.SandboxRuns[3]; !got.Skipped || runners[3].calls != 1 {
		t.Fatalf("staticcheck run = %+v, calls = %d, want invoked skip", got, runners[3].calls)
	}
}

func TestGovernanceIsolatedSandboxCreationFailureOnlySkipsCurrentCommand(t *testing.T) {
	factoryCalls := 0
	var runners []*isolatedSandboxTestRunner
	result, err := runGovernance(
		context.Background(),
		config{enableStaticcheck: true, effectiveRuntime: runtimeE2B},
		reviewInput{
			kind:                     inputKindRepoPath,
			repoRoot:                 t.TempDir(),
			sandboxRepoRoot:          t.TempDir(),
			sandboxDiagnosticModules: testRootDiagnosticModules(),
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunnerFactory: func(context.Context) (sandboxRunner, error) {
			factoryCalls++
			if factoryCalls == 3 {
				return nil, errors.New("sandbox unavailable")
			}
			runner := &isolatedSandboxTestRunner{id: factoryCalls}
			runners = append(runners, runner)
			return runner, nil
		}},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if factoryCalls != 4 || result.ToolCalls != 3 || len(result.SandboxRuns) != 4 {
		t.Fatalf("factory calls = %d, tool calls = %d, runs = %d", factoryCalls, result.ToolCalls, len(result.SandboxRuns))
	}
	if !result.SandboxRuns[2].Skipped || result.SandboxRuns[2].Command != string(commandCheckGoVet) {
		t.Fatalf("vet run = %+v, want isolated creation skip", result.SandboxRuns[2])
	}
	if result.SandboxRuns[3].Skipped || result.SandboxRuns[3].Command != string(commandCheckStaticcheck) {
		t.Fatalf("staticcheck run = %+v, want retry in a new sandbox", result.SandboxRuns[3])
	}
	for _, runner := range runners {
		if runner.calls != 1 || runner.closes != 1 {
			t.Fatalf("runner %+v was not used and closed exactly once", runner)
		}
	}
}

func TestGovernanceIsolatedSandboxPreflightCreationFailureSkipsAllChecks(t *testing.T) {
	factoryCalls := 0
	result, err := runGovernance(
		context.Background(),
		config{enableStaticcheck: true, effectiveRuntime: runtimeE2B},
		reviewInput{
			kind:                     inputKindRepoPath,
			repoRoot:                 t.TempDir(),
			sandboxRepoRoot:          t.TempDir(),
			sandboxDiagnosticModules: testRootDiagnosticModules(),
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunnerFactory: func(context.Context) (sandboxRunner, error) {
			factoryCalls++
			return nil, errors.New("preflight unavailable")
		}},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if factoryCalls != 1 || result.ToolCalls != 0 || len(result.SandboxRuns) != 4 {
		t.Fatalf("factory calls = %d, tool calls = %d, runs = %d", factoryCalls, result.ToolCalls, len(result.SandboxRuns))
	}
	for _, run := range result.SandboxRuns {
		if !run.Skipped {
			t.Fatalf("sandbox run = %+v, want preflight skip", run)
		}
	}
}

func TestLocalRepositoryChecksUseFreshSnapshotAfterTestMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local skill workspace symlink staging is unsupported on Windows")
	}
	if err := preflightLocalRuntime(); err != nil {
		t.Skipf("local runtime unavailable: %v", err)
	}
	for _, failTest := range []bool{false, true} {
		t.Run(fmt.Sprintf("test_failure_%t", failTest), func(t *testing.T) {
			repoRoot := t.TempDir()
			markerPath := filepath.Join(t.TempDir(), "mutation.marker")
			goMod := "module example.com/isolation\n\ngo 1.21\n"
			hello := "package hello\n\nfunc message() string { return \"hello\" }\n"
			failure := ""
			if failTest {
				failure = `t.Fatal("intentional failure after mutation")`
			}
			mutator := fmt.Sprintf(`package hello

import (
	"os"
	"testing"
)

func TestMutateRepository(t *testing.T) {
	if err := os.WriteFile(%q, []byte("ran"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("hello.go", []byte("package broken\nfunc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("not a module\n"), 0600); err != nil {
		t.Fatal(err)
	}
	%s
}
`, markerPath, failure)
			mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), goMod)
			mustWriteFile(t, filepath.Join(repoRoot, "hello.go"), hello)
			mustWriteFile(t, filepath.Join(repoRoot, "mutator_test.go"), mutator)
			mustRunGit(t, repoRoot, "init")
			mustRunGit(t, repoRoot, "add", "go.mod", "hello.go", "mutator_test.go")

			result, err := runGovernance(
				context.Background(),
				config{effectiveRuntime: runtimeLocal},
				reviewInput{kind: inputKindRepoPath, repoRoot: repoRoot},
				parseUnifiedDiff([]byte(minimalDiff())),
				runtimeHooks{},
			)
			if err != nil {
				t.Fatalf("run governance: %v", err)
			}
			testRun := sandboxRunForKind(t, result.SandboxRuns, commandCheckGoTest)
			vetRun := sandboxRunForKind(t, result.SandboxRuns, commandCheckGoVet)
			if failTest && testRun.ExitCode == 0 {
				t.Fatalf("go test run = %+v, want intentional failure", testRun)
			}
			if !failTest && sandboxRunFailed(testRun) {
				t.Fatalf("go test run = %+v, want success", testRun)
			}
			if _, err := os.Stat(markerPath); err != nil {
				t.Fatalf("go test did not execute repository mutation: %v", err)
			}
			if failTest && !strings.Contains(
				testRun.Stdout+"\n"+testRun.Stderr,
				"intentional failure after mutation",
			) {
				t.Fatalf("go test run = %+v, want intentional failure output", testRun)
			}
			if sandboxRunFailed(vetRun) {
				t.Fatalf("go vet saw test-mutated repository: %+v", vetRun)
			}
			assertFileContents(t, filepath.Join(repoRoot, "go.mod"), goMod)
			assertFileContents(t, filepath.Join(repoRoot, "hello.go"), hello)
		})
	}
}

type isolatedSandboxTestRunner struct {
	id       int
	calls    int
	closes   int
	closeErr error
	runErr   error
	skipped  bool
	spec     commandSpec
}

func (r *isolatedSandboxTestRunner) RunSandboxCommand(_ context.Context, spec commandSpec) sandboxRun {
	r.calls++
	r.spec = spec
	if r.runErr != nil {
		return sandboxRun{
			Runtime:    "isolated-test",
			Command:    string(spec.Kind),
			ExitCode:   -1,
			DurationMS: 1,
			Error:      r.runErr.Error(),
		}
	}
	if r.skipped {
		return sandboxRun{
			Runtime:    "isolated-test",
			Command:    string(spec.Kind),
			ExitCode:   0,
			DurationMS: 1,
			Skipped:    true,
		}
	}
	return sandboxRun{
		Runtime:    "isolated-test",
		Command:    string(spec.Kind),
		ExitCode:   0,
		DurationMS: 1,
	}
}

func (r *isolatedSandboxTestRunner) Close() error {
	r.closes++
	return r.closeErr
}

func sandboxRunForKind(t *testing.T, runs []sandboxRun, kind commandKind) sandboxRun {
	t.Helper()
	for _, run := range runs {
		if run.Command == string(kind) {
			return run
		}
	}
	t.Fatalf("sandbox run %q not found: %+v", kind, runs)
	return sandboxRun{}
}

func assertFileContents(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
