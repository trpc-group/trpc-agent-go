//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dynamicworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLocalRunnerRoutesToolAndAgentCalls(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	var calls []Call
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		calls = append(calls, call)
		switch call.Kind {
		case CallKindTool:
			require.Equal(t, "add", call.Name)
			require.JSONEq(t, `{"a":20,"b":22}`, string(call.Args))
			return json.RawMessage(`42`), nil
		case CallKindAgent:
			require.Empty(t, call.Name)
			require.JSONEq(t, `{"input":{"answer":42},"options":{"template":"reviewer"}}`, string(call.Args))
			return json.RawMessage(`{"text":"approved"}`), nil
		default:
			t.Fatalf("unexpected call kind %q", call.Kind)
			return nil, nil
		}
	})

	result, err := Execute(context.Background(), LocalRunner{}, handler, `
print("starting")
answer = await call_tool("add", a=20, b=22)
review = await agent({"answer": answer}, "reviewer")
return {"answer": answer, "review": review}
`)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	require.JSONEq(t, `{"answer":42,"review":{"text":"approved"}}`, string(result.Value))
	require.Equal(t, "starting\n", result.Stdout)
}

func TestRunWorkflowGuestSendsSourceOverStdin(t *testing.T) {
	stdin := &bytes.Buffer{}
	guest := &workflowGuestProcess{
		process: newExitedWorkflowProcess(),
		stdin:   nopWriteCloser{Writer: stdin},
		stdout: strings.NewReader(
			`{"type":"done","result":{"ok":true}}` + "\n",
		),
		stderr: newLimitedBuffer(workflowGuestCapturedOutputLimit),
		code:   "return {'ok': true}",
	}
	result, err := runWorkflowGuest(
		context.Background(),
		guest,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(result.Value))

	var request protocolMessage
	require.NoError(t, json.NewDecoder(stdin).Decode(&request))
	require.Equal(t, "run", request.Type)
	require.Equal(t, guest.code, request.Code)
}

func TestRunWorkflowGuestReportsSourceWriteError(t *testing.T) {
	guest := &workflowGuestProcess{
		process: newExitedWorkflowProcess(),
		stdin:   nopWriteCloser{Writer: errorWriter{}},
		stdout:  strings.NewReader(""),
		stderr:  newLimitedBuffer(workflowGuestCapturedOutputLimit),
		code:    "return None",
	}
	_, err := runWorkflowGuest(
		context.Background(),
		guest,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
	)
	require.ErrorContains(t, err, "write workflow source")
}

func TestLocalRunnerTransportsWorkflowNearDefaultCodeLimit(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	code := strings.Repeat("# padding\n", 6400) + "return {'ok': true}\n"
	require.Greater(t, len(code), 60<<10)
	require.LessOrEqual(t, len(code), 64<<10)

	result, err := Execute(
		context.Background(),
		LocalRunner{},
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		code,
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(result.Value))
}

func TestLocalRunnerRoutesDynamicAgentSpec(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		require.Equal(t, CallKindAgent, call.Kind)
		require.Empty(t, call.Name)
		require.JSONEq(t, `{
			"options":{
				"template":"reviewer",
				"instance_id":"strict-review",
				"instruction":"Be strict.",
				"tools":["lookup"]
			},
			"input":{"answer":42}
		}`, string(call.Args))
		return json.RawMessage(`{"text":"approved"}`), nil
	})

	result, err := Execute(context.Background(), LocalRunner{}, handler, `
review = await agent({"answer": 42}, {
    "template": "reviewer",
    "instance_id": "strict-review",
    "instruction": "Be strict.",
    "tools": ["lookup"],
})
return review
`)
	require.NoError(t, err)
	require.JSONEq(t, `{"text":"approved"}`, string(result.Value))
}

func TestLocalRunnerRoutesKeywordAgentOptions(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		require.Equal(t, CallKindAgent, call.Kind)
		require.JSONEq(t, `{
			"input": {"answer": 42},
			"options": {
				"instruction": "Be strict.",
				"structured_output": {
					"type": "object",
					"properties": {"approved": {"type": "boolean"}}
				}
			}
		}`, string(call.Args))
		return json.RawMessage(`{"structured":{"approved":true}}`), nil
	})

	result, err := Execute(context.Background(), LocalRunner{}, handler, `
review = await agent(
    {"answer": 42},
    instruction="Be strict.",
    structured_output={
        "type": "object",
        "properties": {"approved": {"type": "boolean"}},
    },
)
return review["approved"]
`)
	require.NoError(t, err)
	require.JSONEq(t, `true`, string(result.Value))
}

func TestLocalRunnerRejectsUnknownKeywordAgentOption(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	_, err := Execute(context.Background(), LocalRunner{}, callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), `
return await agent("review", unsupported_option=True)
`)
	require.ErrorContains(t, err, "unsupported agent option(s): unsupported_option")
}

func TestLocalRunnerRejectsUncalledWorkflowWrapper(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	_, err := Execute(context.Background(), LocalRunner{}, callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), `
async def run():
    return {"status": "not invoked"}
`)
	require.ErrorContains(t, err, "workflow code must contain a return statement outside nested functions or classes")
}

func TestLocalRunnerProjectsStructuredAgentFields(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		require.Equal(t, CallKindAgent, call.Kind)
		return json.RawMessage(`{
			"text":"approved",
			"structured":{"approved":true,"sku":"TRAIL-40"}
		}`), nil
	})

	result, err := Execute(context.Background(), LocalRunner{}, handler, `
review = await agent({"sku": "TRAIL-40"}, "reviewer")
return {
    "explicit": review["structured"]["sku"],
    "index": review["sku"],
    "get": review.get("approved"),
    "text": review["text"],
}
`)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"explicit":"TRAIL-40",
		"index":"TRAIL-40",
		"get":true,
		"text":"approved"
	}`, string(result.Value))
}

func TestLocalRunnerSupportsJSONStyleLiterals(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	result, err := Execute(context.Background(), LocalRunner{}, callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), `
return {"enabled": true, "disabled": false, "value": null}
`)
	require.NoError(t, err)
	require.JSONEq(t, `{"enabled":true,"disabled":false,"value":null}`, string(result.Value))
}

func TestLocalRunnerParallelRoutesAgentCallsConcurrently(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := make(chan struct{})
	var startOnce sync.Once
	var mu sync.Mutex
	startedCount := 0
	handler := callHandlerFunc(func(ctx context.Context, call Call) (json.RawMessage, error) {
		if call.Kind != CallKindAgent {
			return nil, fmt.Errorf("unexpected call kind %q", call.Kind)
		}
		var args struct {
			Input struct {
				ID string `json:"id"`
			} `json:"input"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		mu.Lock()
		startedCount++
		if startedCount == 2 {
			startOnce.Do(func() { close(started) })
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-started:
			return json.RawMessage(fmt.Sprintf(`{"text":%q}`, args.Input.ID)), nil
		}
	})

	result, err := Execute(ctx, LocalRunner{}, handler, `
results = await parallel([
    lambda: agent({"id": "first"}, "reviewer"),
    lambda: agent({"id": "second"}, "reviewer"),
])
return results
`)
	require.NoError(t, err)
	require.JSONEq(t, `[{"text":"first"},{"text":"second"}]`, string(result.Value))
}

func TestLocalRunnerParallelKeepsCompletedBranchesWhenOneFails(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		var args struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		if args.Input == "bad" {
			return nil, errors.New("expected branch failure")
		}
		return json.RawMessage(`{"text":"ok"}`), nil
	})

	result, err := Execute(context.Background(), LocalRunner{}, handler, `
results = await parallel([
    lambda: agent("good", "reviewer"),
    lambda: agent("bad", "reviewer"),
])
return results
`)
	require.NoError(t, err)
	require.JSONEq(t, `[{"text":"ok"},null]`, string(result.Value))
}

func TestLocalRunnerPipelineStreamsEachItemWithoutBatchBarrier(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	releaseAnalysisB := make(chan struct{})
	var releaseOnce sync.Once
	handler := callHandlerFunc(func(ctx context.Context, call Call) (json.RawMessage, error) {
		var args struct {
			Input struct {
				Stage string `json:"stage"`
				File  string `json:"file"`
			} `json:"input"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		switch {
		case args.Input.Stage == "analyze" && args.Input.File == "a":
			return json.RawMessage(`{"structured":{"file":"a"}}`), nil
		case args.Input.Stage == "analyze" && args.Input.File == "b":
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseAnalysisB:
				return json.RawMessage(`{"structured":{"file":"b"}}`), nil
			}
		case args.Input.Stage == "review" && args.Input.File == "a":
			releaseOnce.Do(func() { close(releaseAnalysisB) })
			return json.RawMessage(`{"text":"reviewed-a"}`), nil
		case args.Input.Stage == "review" && args.Input.File == "b":
			return json.RawMessage(`{"text":"reviewed-b"}`), nil
		default:
			return nil, fmt.Errorf("unexpected pipeline call: %s/%s", args.Input.Stage, args.Input.File)
		}
	})

	result, err := Execute(ctx, LocalRunner{}, handler, `
async def analyze(previous, original, index):
    return await agent({"stage": "analyze", "file": previous}, "reviewer")

async def review(analysis, original, index):
    return await agent({
        "stage": "review",
        "file": original,
        "analysis": analysis["structured"]["file"],
    }, "reviewer")

return await pipeline(["a", "b"], analyze, review)
`)
	require.NoError(t, err)
	require.JSONEq(t, `[{"text":"reviewed-a"},{"text":"reviewed-b"}]`, string(result.Value))
}

func TestLocalRunnerOptionalTimeoutStopsBusyWorkflow(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}

	start := time.Now()
	_, err := Execute(context.Background(), NewLocalRunner(LocalRunnerConfig{Timeout: 100 * time.Millisecond}), callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), `
while True:
    pass
`)
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestConfiguredLocalRunnerTimeoutCancelsCallback(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	started := time.Now()
	_, err := Execute(
		context.Background(),
		NewLocalRunner(LocalRunnerConfig{Timeout: 100 * time.Millisecond}),
		callHandlerFunc(func(ctx context.Context, _ Call) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		`return await agent({"task": "wait"})`,
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 5*time.Second)
}

func TestFinishWorkflowGuestPreservesCompletedResultAfterExitTimeout(t *testing.T) {
	process := newBlockingWorkflowProcess()
	guest := newFakeWorkflowGuest(
		process,
		`{"type":"done","result":{"ok":true}}`+"\n",
	)
	result, err := runWorkflowGuest(
		context.Background(),
		guest,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(result.Value))
	require.NoError(t, waitClosed(process.killed, time.Second))
}

func TestFinishWorkflowGuestCancelsCallbacksAfterCompletion(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	guest := newFakeWorkflowGuest(
		newExitedWorkflowProcess(),
		strings.Join([]string{
			`{"type":"call","id":"1","kind":"agent","args":{}}`,
			`{"type":"done","result":{"ok":true}}`,
			"",
		}, "\n"),
	)
	result, err := runWorkflowGuest(
		context.Background(),
		guest,
		callHandlerFunc(func(ctx context.Context, _ Call) (json.RawMessage, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}),
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(result.Value))
	require.NoError(t, waitClosed(started, time.Second))
	require.NoError(t, waitClosed(cancelled, time.Second))
}

func TestFinishWorkflowGuestReportsStderrLimit(t *testing.T) {
	guest := newFakeWorkflowGuest(
		newExitedWorkflowProcess(),
		`{"type":"done","result":null}`+"\n",
	)
	_, err := guest.stderr.Write(bytes.Repeat(
		[]byte("x"),
		workflowGuestCapturedOutputLimit+1,
	))
	require.NoError(t, err)

	_, err = runWorkflowGuest(
		context.Background(),
		guest,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
	)
	require.ErrorContains(t, err, "guest stderr exceeds")
}

func TestLocalRunnerEnforcesOutputLimits(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	handler := callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	})

	_, err := Execute(context.Background(), LocalRunner{}, handler, fmt.Sprintf(`
print("x" * %d)
return None
`, workflowGuestCapturedOutputLimit+1))
	require.ErrorContains(t, err, "workflow stdout exceeds")

	_, err = Execute(context.Background(), LocalRunner{}, handler, fmt.Sprintf(`
return "x" * %d
`, workflowGuestProtocolLineLimit+1))
	require.ErrorContains(t, err, "workflow protocol message exceeds")
}

func TestLocalRunnerRejectsRestrictedPythonFeatures(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	handler := callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	})

	cases := []struct {
		name string
		code string
		want string
	}{
		{
			name: "import",
			code: "import os\nreturn None",
			want: "unsupported Python syntax: Import",
		},
		{
			name: "from import",
			code: "from os import system\nreturn None",
			want: "unsupported Python syntax: ImportFrom",
		},
		{
			name: "class",
			code: "class Unsafe:\n    pass\nreturn None",
			want: "unsupported Python syntax: ClassDef",
		},
		{
			name: "with",
			code: "with [] as value:\n    return value\nreturn None",
			want: "unsupported Python syntax: With",
		},
		{
			name: "try",
			code: "try:\n    return None\nexcept Exception:\n    return None",
			want: "unsupported Python syntax: Try",
		},
		{
			name: "open",
			code: `return open("/etc/passwd").read()`,
			want: "restricted function: open",
		},
		{
			name: "open reference",
			code: "fn = open\nreturn fn",
			want: "restricted name: open",
		},
		{
			name: "eval",
			code: `return eval("1 + 1")`,
			want: "restricted function: eval",
		},
		{
			name: "import builtin",
			code: `return __import__("os")`,
			want: "restricted function: __import__",
		},
		{
			name: "dunder attribute",
			code: "return (1).__class__",
			want: "dunder attributes: __class__",
		},
		{
			name: "dunder name",
			code: "return __builtins__",
			want: "dunder names: __builtins__",
		},
		{
			name: "globals",
			code: "return globals()",
			want: "restricted function: globals",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Execute(context.Background(), LocalRunner{}, handler, tc.code)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestLocalRunnerAllowsHarmlessRestrictedNameStrings(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	result, err := Execute(context.Background(), LocalRunner{}, callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), `
name = "open"
return {"name": name}
`)
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"open"}`, string(result.Value))
}

func TestLocalRunnerEnforcesMaxCodeBytes(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	_, err := Execute(
		context.Background(),
		NewLocalRunner(LocalRunnerConfig{MaxCodeBytes: 8}),
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"return None",
	)
	require.ErrorContains(t, err, "code exceeds 8 bytes")
}

func TestLocalRunnerWorkDirCannotShadowBootstrapImports(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	workDir := t.TempDir()
	marker := filepath.Join(workDir, "import-shadowed")
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "asyncio.py"),
		[]byte("from pathlib import Path\nPath('import-shadowed').write_text('loaded')\n"),
		0o600,
	))

	result, err := Execute(
		context.Background(),
		NewLocalRunner(LocalRunnerConfig{WorkDir: workDir}),
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"return None",
	)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(result.Value))
	require.NoFileExists(t, marker)
}

func TestExecuteRejectsMissingRequirements(t *testing.T) {
	_, err := Execute(context.Background(), nil, callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), "return None")
	require.ErrorContains(t, err, "runtime is required")

	_, err = Execute(context.Background(), LocalRunner{}, nil, "return None")
	require.ErrorContains(t, err, "call handler is required")

	_, err = Execute(context.Background(), LocalRunner{}, callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return nil, nil
	}), " ")
	require.ErrorContains(t, err, "code is required")
}

func TestLocalRunnerStartFailureIncludesPythonError(t *testing.T) {
	_, err := Execute(
		context.Background(),
		LocalRunner{Python: "python-that-does-not-exist"},
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"return None",
	)
	require.ErrorContains(t, err, "start Python guest")
}

func TestWorkflowGuestProtocolHelpers(t *testing.T) {
	handler := callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	})
	encoder := json.NewEncoder(io.Discard)
	responseMu := &sync.Mutex{}
	calls := &sync.WaitGroup{}
	writeErr := &workflowWriteError{}
	state := &workflowGuestState{}

	stop := processWorkflowGuestMessage(
		context.Background(),
		[]byte(`{`),
		handler,
		encoder,
		responseMu,
		calls,
		writeErr,
		state,
		nil,
	)
	require.True(t, stop)
	require.ErrorContains(t, state.guestErr, "malformed guest message")

	state = &workflowGuestState{}
	stop = handleWorkflowGuestMessage(
		context.Background(),
		protocolMessage{Type: "done", Result: json.RawMessage(`not-json`)},
		handler,
		encoder,
		responseMu,
		calls,
		writeErr,
		state,
		nil,
	)
	require.True(t, stop)
	require.ErrorContains(t, state.guestErr, "non-JSON result")

	state = &workflowGuestState{}
	stop = handleWorkflowGuestMessage(
		context.Background(),
		protocolMessage{Type: "error", Error: "guest failed"},
		handler,
		encoder,
		responseMu,
		calls,
		writeErr,
		state,
		nil,
	)
	require.True(t, stop)
	require.ErrorContains(t, state.guestErr, "guest failed")

	state = &workflowGuestState{}
	stop = handleWorkflowGuestMessage(
		context.Background(),
		protocolMessage{Type: "done", Result: json.RawMessage(`{"ok":true}`), Stdout: "hello\n"},
		handler,
		encoder,
		responseMu,
		calls,
		writeErr,
		state,
		nil,
	)
	require.True(t, stop)
	require.NoError(t, state.guestErr)
	require.JSONEq(t, `{"ok":true}`, string(state.completed.Value))
	require.Equal(t, "hello\n", state.completed.Stdout)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state = &workflowGuestState{}
	stop = handleWorkflowGuestMessage(
		ctx,
		protocolMessage{Type: "call", ID: "blocked"},
		handler,
		encoder,
		responseMu,
		calls,
		writeErr,
		state,
		make(chan struct{}),
	)
	require.True(t, stop)
	require.ErrorContains(t, state.guestErr, "acquire callback slot")
}

func TestWorkflowGuestWriteResponseRecordsFirstError(t *testing.T) {
	encoder := json.NewEncoder(errorWriter{})
	responseMu := &sync.Mutex{}
	writeErr := &workflowWriteError{}

	writeWorkflowGuestResponse(
		encoder,
		responseMu,
		writeErr,
		protocolMessage{Type: "result", ID: "1", Result: json.RawMessage(`true`)},
	)
	writeWorkflowGuestResponse(
		encoder,
		responseMu,
		writeErr,
		protocolMessage{Type: "result", ID: "2", Result: json.RawMessage(`false`)},
	)

	writeErr.Lock()
	defer writeErr.Unlock()
	require.ErrorContains(t, writeErr.err, "write failed")
}

type callHandlerFunc func(context.Context, Call) (json.RawMessage, error)

func (f callHandlerFunc) HandleWorkflowCall(ctx context.Context, call Call) (json.RawMessage, error) {
	return f(ctx, call)
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type fakeWorkflowProcess struct {
	wait   <-chan struct{}
	killed chan struct{}
	once   sync.Once
}

func newBlockingWorkflowProcess() *fakeWorkflowProcess {
	released := make(chan struct{})
	return &fakeWorkflowProcess{wait: released, killed: released}
}

func newExitedWorkflowProcess() *fakeWorkflowProcess {
	exited := make(chan struct{})
	close(exited)
	return &fakeWorkflowProcess{wait: exited, killed: make(chan struct{})}
}

func (p *fakeWorkflowProcess) Wait() error {
	<-p.wait
	return nil
}

func (p *fakeWorkflowProcess) Kill() error {
	p.once.Do(func() { close(p.killed) })
	return nil
}

func newFakeWorkflowGuest(
	process workflowProcess,
	stdout string,
) *workflowGuestProcess {
	return &workflowGuestProcess{
		process: process,
		stdin:   nopWriteCloser{Writer: &bytes.Buffer{}},
		stdout:  strings.NewReader(stdout),
		stderr:  newLimitedBuffer(workflowGuestCapturedOutputLimit),
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func waitClosed(ch <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-timer.C:
		return fmt.Errorf("timed out waiting for channel close")
	}
}
