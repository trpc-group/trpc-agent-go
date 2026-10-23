// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package responses

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/v3/option"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	legacychat "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	recoveryAppName       = "crash-recovery-check"
	recoveryModelName     = "test-model"
	recoveryToolCallID    = "call_pending"
	recoveryChildDirEnv   = "TRPC_RECOVERY_TEST_DIR"
	recoveryChildPhaseEnv = "TRPC_RECOVERY_TEST_PHASE"
	recoveryChildPointEnv = "TRPC_RECOVERY_TEST_POINT"
	recoveryChildProtoEnv = "TRPC_RECOVERY_TEST_PROTOCOL"
	recoveryChildBaseEnv  = "TRPC_RECOVERY_TEST_BASE_URL"
	recoveryChildIdemEnv  = "TRPC_RECOVERY_TEST_IDEMPOTENT"
)

const recoveryNativeItems = `[{"type":"reasoning","id":"rs_pending","summary":[],"encrypted_content":"opaque-fixture-state"},{"type":"function_call","id":"fc_pending","call_id":"call_pending","status":"completed","name":"calculator","arguments":"{\"n\":38,\"multiplier\":2}"}]`

type recoveryCalculatorArgs struct {
	N          int `json:"n"`
	Multiplier int `json:"multiplier"`
}

type recoveryChildProcess struct {
	cmd    *exec.Cmd
	ready  chan string
	done   chan error
	exited chan struct{}
	output bytes.Buffer
	mu     sync.Mutex
}

func recoveryOpenSession(t *testing.T, dir string, hooks ...session.AppendEventHook) *sqlite.Service {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "session.db")+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false), sqlite.WithAppendEventHook(hooks...))
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return svc
}

func recoveryOpenLedger(dir string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath.Join(dir, "effects.db")+"?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	for _, query := range []string{
		"CREATE TABLE IF NOT EXISTS attempts (id INTEGER PRIMARY KEY, key TEXT)",
		"CREATE TABLE IF NOT EXISTS effects (key TEXT PRIMARY KEY, result INTEGER)",
	} {
		if _, err = db.Exec(query); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

func recoveryLedgerCounts(t *testing.T, dir string) (attempts, effects int) {
	t.Helper()
	db, err := recoveryOpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow("SELECT COUNT(*) FROM attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM effects").Scan(&effects); err != nil {
		t.Fatal(err)
	}
	return attempts, effects
}

func recoveryHold(point string) {
	fmt.Println("READY " + point)
	for {
		time.Sleep(time.Hour)
	}
}

func recoveryCalculator(dir, phase, point string, idempotent bool) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, input recoveryCalculatorArgs) (int, error) {
		if input.N != 38 || input.Multiplier != 2 {
			return 0, fmt.Errorf("unexpected calculator arguments")
		}
		db, err := recoveryOpenLedger(dir)
		if err != nil {
			return 0, err
		}
		defer db.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, "INSERT INTO attempts(key) VALUES (?)", "logical-call-pending"); err != nil {
			return 0, err
		}
		effectKey := "logical-call-pending"
		if !idempotent {
			effectKey = fmt.Sprintf("effect-%d", time.Now().UnixNano())
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO effects(key,result) VALUES (?,?)", effectKey, 76); err != nil {
			return 0, err
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		if phase == "fresh" && point == "after_effect" {
			recoveryHold(point)
		}
		return 76, nil
	}, function.WithName("calculator"), function.WithDescription("Multiply n by multiplier."))
}

// TestRecoveryChildRunner is entered only by a child test process; its parent
// either kills it at the selected durable cutpoint or waits for resume to end.
func TestRecoveryChildRunner(t *testing.T) {
	dir := os.Getenv(recoveryChildDirEnv)
	if dir == "" {
		t.Skip("child-process helper")
	}
	phase := os.Getenv(recoveryChildPhaseEnv)
	point := os.Getenv(recoveryChildPointEnv)
	protocol := os.Getenv(recoveryChildProtoEnv)
	baseURL := os.Getenv(recoveryChildBaseEnv)
	idempotent := os.Getenv(recoveryChildIdemEnv) != "false"
	var selectedModel model.Model
	if protocol == "responses" {
		selectedModel = New(recoveryModelName,
			WithAPIKey("offline-test"),
			WithBaseURL(baseURL),
			WithOpenAIOptions(option.WithMaxRetries(0)),
		)
	} else {
		selectedModel = legacychat.New(recoveryModelName,
			legacychat.WithAPIKey("offline-test"),
			legacychat.WithBaseURL(baseURL),
		)
	}
	hooks := []session.AppendEventHook{func(hookCtx *session.AppendEventContext, next func() error) error {
		if err := next(); err != nil {
			return err
		}
		if phase == "fresh" && point == "after_result" && hookCtx.Event != nil && hookCtx.Event.IsToolResultResponse() {
			recoveryHold(point)
		}
		return nil
	}}
	svc := recoveryOpenSession(t, dir, hooks...)
	defer svc.Close()
	callbacks := tool.NewCallbacks().RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		if phase == "fresh" && point == "before_tool" {
			recoveryHold(point)
		}
		return nil, nil
	})
	ag := llmagent.New(
		recoveryAppName,
		llmagent.WithModel(selectedModel),
		llmagent.WithTools([]tool.Tool{recoveryCalculator(dir, phase, point, idempotent)}),
		llmagent.WithToolCallbacks(callbacks),
		llmagent.WithMaxLLMCalls(2),
		llmagent.WithInstruction("Use calculator, then return the result only."),
	)
	r := runner.NewRunner(recoveryAppName, ag, runner.WithSessionService(svc))
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	message := model.NewUserMessage("Calculate 38 times 2 using calculator.")
	var runOptions []agent.RunOption
	if phase == "resume" {
		message = model.Message{}
		runOptions = append(runOptions, agent.WithResume(true))
	}
	events, err := r.Run(ctx, "user", "session", message, runOptions...)
	if err != nil {
		t.Fatal(err)
	}
	answer := ""
	completions := 0
	for evt := range events {
		if evt.Error != nil {
			t.Fatalf("runner error: %s: %s", evt.Error.Type, evt.Error.Message)
		}
		if evt.IsRunnerCompletion() {
			completions++
		}
		if !evt.IsPartial {
			for _, choice := range evt.Choices {
				if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
					answer = choice.Message.Content
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	if phase == "resume" && answer != "76" {
		t.Fatalf("unexpected final answer %q", answer)
	}
	if phase == "resume" && completions != 1 {
		t.Fatalf("runner completion events=%d, want 1", completions)
	}
	fmt.Println("RESUMED " + answer)
}

func recoveryStartChild(t *testing.T, dir, phase, point, protocol, baseURL string, idempotent bool) *recoveryChildProcess {
	t.Helper()
	child := &recoveryChildProcess{ready: make(chan string, 1), done: make(chan error, 1), exited: make(chan struct{})}
	child.cmd = exec.Command(os.Args[0], "-test.run=^TestRecoveryChildRunner$", "-test.v")
	child.cmd.Env = append(os.Environ(),
		recoveryChildDirEnv+"="+dir,
		recoveryChildPhaseEnv+"="+phase,
		recoveryChildPointEnv+"="+point,
		recoveryChildProtoEnv+"="+protocol,
		recoveryChildBaseEnv+"="+baseURL,
		fmt.Sprintf("%s=%t", recoveryChildIdemEnv, idempotent),
	)
	pipe, err := child.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.cmd.Stderr = child.cmd.Stdout
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(child.exited)
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			child.mu.Lock()
			child.output.WriteString(line + "\n")
			child.mu.Unlock()
			if strings.HasPrefix(line, "READY ") {
				select {
				case child.ready <- strings.TrimPrefix(line, "READY "):
				default:
				}
			}
		}
		scanErr := scanner.Err()
		waitErr := child.cmd.Wait()
		if scanErr != nil {
			child.done <- fmt.Errorf("scan child output: %w (process wait: %v)", scanErr, waitErr)
			return
		}
		child.done <- waitErr
	}()
	t.Cleanup(func() {
		if child.cmd.Process == nil {
			return
		}
		select {
		case <-child.exited:
			return
		default:
		}
		_ = child.cmd.Process.Kill()
		select {
		case <-child.exited:
		case <-time.After(3 * time.Second):
			t.Errorf("child cleanup timed out\n%s", child.log())
		}
	})
	return child
}

func recoveryWaitForDurableCall(t *testing.T, svc *sqlite.Service) *session.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		sess, err := svc.GetSession(ctx, session.Key{AppName: recoveryAppName, UserID: "user", SessionID: "session"})
		if err != nil {
			t.Fatal(err)
		}
		for _, evt := range sess.Events {
			if evt.Response != nil && evt.IsToolCallResponse() {
				return sess
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("tool call did not become durable while the child was paused")
		case <-ticker.C:
		}
	}
}

func (child *recoveryChildProcess) log() string {
	child.mu.Lock()
	defer child.mu.Unlock()
	return child.output.String()
}

func recoveryExpectReady(t *testing.T, child *recoveryChildProcess, point string) {
	t.Helper()
	select {
	case ready := <-child.ready:
		if ready != point {
			t.Fatalf("child stopped at %q, want %q", ready, point)
		}
	case err := <-child.done:
		t.Fatalf("child exited before cutpoint: %v\n%s", err, child.log())
	case <-time.After(10 * time.Second):
		t.Fatalf("child did not reach cutpoint within 10s\n%s", child.log())
	}
}

func recoveryWaitChild(t *testing.T, child *recoveryChildProcess) {
	t.Helper()
	select {
	case err := <-child.done:
		if err != nil {
			t.Fatalf("resume child failed: %v\n%s", err, child.log())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("resume child did not exit within 20s\n%s", child.log())
	}
}

func recoveryContainsItems(input []any, expected []any) bool {
	if len(expected) == 0 || len(input) < len(expected) {
		return false
	}
	for i := 0; i+len(expected) <= len(input); i++ {
		if reflect.DeepEqual(input[i:i+len(expected)], expected) {
			return true
		}
	}
	return false
}

// TestRunnerCrossProcessRecovery runs eight isolated fresh/resume subprocess
// cases and normally finishes in a few seconds. Cutpoint waits are capped at
// 10s and each child Runner has a 15s deadline; it is compatible with -race.
func TestRunnerCrossProcessRecovery(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		for _, point := range []string{"before_tool", "after_result", "after_effect"} {
			for _, idempotent := range []bool{true, false} {
				if point != "after_effect" && !idempotent {
					continue
				}
				caseName := fmt.Sprintf("%s/%s/idempotent_%t", protocol, point, idempotent)
				t.Run(caseName, func(t *testing.T) {
					dir := t.TempDir()
					var requestCount atomic.Int32
					var mu sync.Mutex
					var requests []map[string]any
					var problems []string
					expectedNative := make([]any, 0, 2)
					if err := json.Unmarshal([]byte(recoveryNativeItems), &expectedNative); err != nil {
						t.Fatal(err)
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n := requestCount.Add(1)
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							mu.Lock()
							problems = append(problems, "read model request: "+err.Error())
							mu.Unlock()
							http.Error(w, "invalid request", http.StatusBadRequest)
							return
						}
						var req map[string]any
						if err = json.Unmarshal(raw, &req); err != nil {
							mu.Lock()
							problems = append(problems, "decode model request: "+err.Error())
							mu.Unlock()
							http.Error(w, "invalid request json", http.StatusBadRequest)
							return
						}
						mu.Lock()
						requests = append(requests, req)
						mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						if n > 2 {
							http.Error(w, "model request limit exceeded", http.StatusBadRequest)
							return
						}
						if protocol == "responses" {
							if n == 1 {
								_, _ = io.WriteString(w, `{"id":"resp_pending","status":"completed","output":`+recoveryNativeItems+`}`)
								return
							}
							input, _ := req["input"].([]any)
							resultFound := false
							for _, item := range input {
								m, _ := item.(map[string]any)
								if m["type"] == "function_call_output" && m["call_id"] == recoveryToolCallID && m["output"] == "76" {
									resultFound = true
								}
							}
							if !recoveryContainsItems(input, expectedNative) || !resultFound {
								mu.Lock()
								problems = append(problems, "native items or matching tool result lost during Runner recovery")
								mu.Unlock()
							}
							_, _ = io.WriteString(w, `{"id":"resp_answer","status":"completed","output":[{"type":"message","id":"msg_answer","status":"completed","role":"assistant","content":[{"type":"output_text","text":"76"}]}]}`)
							return
						}
						if n == 1 {
							_, _ = io.WriteString(w, `{"id":"chat_pending","object":"chat.completion","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call_pending","type":"function","function":{"name":"calculator","arguments":"{\"n\":38,\"multiplier\":2}"}}]}}]}`)
							return
						}
						messages, _ := req["messages"].([]any)
						callFound, resultFound := false, false
						for _, item := range messages {
							m, _ := item.(map[string]any)
							if m["role"] == "assistant" {
								calls, _ := m["tool_calls"].([]any)
								for _, call := range calls {
									toolCall, _ := call.(map[string]any)
									if toolCall["id"] == recoveryToolCallID {
										callFound = true
									}
								}
							}
							if m["role"] == "tool" && m["tool_call_id"] == recoveryToolCallID && m["content"] == "76" {
								resultFound = true
							}
						}
						if !callFound || !resultFound {
							mu.Lock()
							problems = append(problems, "Chat tool history lost during Runner recovery")
							mu.Unlock()
						}
						_, _ = io.WriteString(w, `{"id":"chat_answer","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"76"}}]}`)
					}))
					t.Cleanup(server.Close)

					first := recoveryStartChild(t, dir, "fresh", point, protocol, server.URL, idempotent)
					recoveryExpectReady(t, first, point)
					// Verify each durable boundary while the fresh process is still alive.
					svc := recoveryOpenSession(t, dir)
					// Legacy model events need not wait for persistence before the
					// BeforeTool callback. Observe the durable boundary explicitly.
					sess := recoveryWaitForDurableCall(t, svc)
					if err := svc.Close(); err != nil {
						t.Fatal(err)
					}
					callRecords, resultRecords := 0, 0
					for _, evt := range sess.Events {
						if evt.Response == nil {
							continue
						}
						if evt.IsToolCallResponse() {
							callRecords++
							if protocol == "responses" {
								for _, choice := range evt.Choices {
									payload, err := choice.Message.ReplayDataFor("openai.responses", recoveryModelName)
									if err != nil {
										t.Fatal(err)
									}
									if len(payload) == 0 {
										t.Fatal("native replay missing at cutpoint")
									}
								}
							}
						}
						if evt.IsToolResultResponse() {
							resultRecords++
						}
					}
					if callRecords != 1 {
						t.Fatalf("durable tool call records=%d, want 1", callRecords)
					}
					wantResultRecords := 0
					if point == "after_result" {
						wantResultRecords = 1
					}
					if resultRecords != wantResultRecords {
						t.Fatalf("durable tool result records=%d, want %d", resultRecords, wantResultRecords)
					}
					attemptsBefore, effectsBefore := recoveryLedgerCounts(t, dir)
					if point == "before_tool" {
						if attemptsBefore != 0 || effectsBefore != 0 {
							t.Fatalf("tool ran before cutpoint: attempts=%d effects=%d", attemptsBefore, effectsBefore)
						}
					} else if attemptsBefore != 1 || effectsBefore != 1 {
						t.Fatalf("unexpected pre-crash ledger: attempts=%d effects=%d", attemptsBefore, effectsBefore)
					}

					if err := first.cmd.Process.Kill(); err != nil {
						t.Fatal(err)
					}
					select {
					case err := <-first.done:
						if err == nil {
							t.Fatal("fresh child exited normally; expected forced process kill")
						}
					case <-time.After(3 * time.Second):
						t.Fatalf("SIGKILL did not stop fresh child\n%s", first.log())
					}

					second := recoveryStartChild(t, dir, "resume", point, protocol, server.URL, idempotent)
					recoveryWaitChild(t, second)
					attempts, effects := recoveryLedgerCounts(t, dir)
					wantAttempts, wantEffects := 1, 1
					if point == "after_effect" {
						wantAttempts = 2
						if !idempotent {
							wantEffects = 2
						}
					}
					if attempts != wantAttempts || effects != wantEffects {
						t.Fatalf("ledger attempts/effects=%d/%d, want %d/%d", attempts, effects, wantAttempts, wantEffects)
					}
					mu.Lock()
					gotRequests := requestCount.Load()
					gotProblems := append([]string(nil), problems...)
					mu.Unlock()
					if gotRequests != 2 {
						t.Fatalf("model requests=%d, want 2", gotRequests)
					}
					if len(gotProblems) > 0 {
						t.Fatal(gotProblems)
					}
					t.Logf("protocol=%s cutpoint=%s idempotent=%t tool_attempts=%d external_effects=%d model_requests=%d native_history_checked=%t",
						protocol, point, idempotent, attempts, effects, gotRequests, protocol == "responses")
				})
			}
		}
	}
}
