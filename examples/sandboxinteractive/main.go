//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates sandboxed interactive execution in two ways:
// direct coordinator calls and model-driven workspace_exec tool calls.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	mode = flag.String(
		"mode",
		"model",
		"Demo mode: direct or model",
	)
	workRoot = flag.String(
		"work-root",
		"",
		"Optional base directory used for created workspaces",
	)
	timeout = flag.Duration(
		"timeout",
		45*time.Second,
		"Interactive command timeout",
	)
	modelName = flag.String(
		"model",
		"deepseek-chat",
		"Model name used in model mode",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming responses in model mode",
	)
	request = flag.String(
		"request",
		"",
		"Optional initial user request sent before entering interactive chat in model mode",
	)
)

const sampleModelRequest = `Use workspace_exec to demonstrate an interactive sandbox session.

Requirements:
1. Start an interactive shell command with tty=true and background=true.
2. The command should print "name: ", read one line, then print:
   - hello:<name>
   - workspace:$WORKSPACE_DIR
3. After the session starts, use workspace_write_stdin with append_newline=true
   to send the name "gpt".
4. Wait for the command to finish and then summarize the observed result.

Do not answer without calling the tools.`

func main() {
	flag.Parse()

	reader := bufio.NewReader(os.Stdin)
	ctx := context.Background()

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "direct":
		if err := runDirectDemo(ctx, reader); err != nil {
			log.Fatalf("direct demo failed: %v", err)
		}
	case "model":
		if err := runModelDemo(ctx, reader); err != nil {
			log.Fatalf("model demo failed: %v", err)
		}
	default:
		log.Fatalf("unknown -mode=%q (supported: direct, model)", *mode)
	}
}

func runDirectDemo(ctx context.Context, reader *bufio.Reader) error {
	rt := local.NewRuntime(*workRoot)
	ws, err := rt.CreateWorkspace(
		ctx,
		"sandbox-interactive-example",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	defer func() {
		if err := rt.Cleanup(ctx, ws); err != nil {
			log.Printf("cleanup workspace: %v", err)
		}
	}()

	coordinator := newSandboxCoordinator(
		reader,
		os.Stdout,
		local.NewSandboxBackend(rt),
	)

	fmt.Println("=== Sandbox Interactive Demo (Direct Mode) ===")
	fmt.Printf("Workspace: %s\n", ws.Path)
	fmt.Printf("TTY enabled: %t\n", runtime.GOOS != "windows")
	fmt.Println()

	if err := runDeniedExample(ctx, coordinator, ws, *timeout); err != nil {
		return err
	}
	fmt.Println()

	return runApprovedDirectExample(
		ctx,
		reader,
		coordinator,
		ws,
		*timeout,
	)
}

func runModelDemo(ctx context.Context, reader *bufio.Reader) error {
	coordinator := newSandboxCoordinator(reader, os.Stdout)
	opts := []local.CodeExecutorOption{
		local.WithTimeout(*timeout),
		local.WithSandboxCoordinator(coordinator),
	}
	if strings.TrimSpace(*workRoot) != "" {
		opts = append(opts, local.WithWorkDir(*workRoot))
	}
	exec := local.New(opts...)

	agt := llmagent.New(
		"sandboxinteractive-assistant",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription(
			"An assistant that demonstrates sandboxed interactive workspace execution",
		),
		llmagent.WithInstruction(modelDemoInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(2000),
			Stream:    *streaming,
		}),
		llmagent.WithCodeExecutor(exec),
	)

	r := runner.NewRunner(
		"sandboxinteractive-model-demo",
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("sandboxinteractive-%d", time.Now().Unix())
	fmt.Println("=== Sandbox Interactive Demo (Model Mode) ===")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Println("Chat with the agent. If it decides to start an interactive")
	fmt.Println("workspace_exec session, the approval prompt will appear naturally")
	fmt.Println("during the same turn.")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Commands:")
	fmt.Println("  /demo  - send the built-in interactive workspace_exec request")
	fmt.Println("  /exit  - quit")
	fmt.Println()
	fmt.Printf("Sample Prompt:\n%s\n", sampleModelRequest)
	fmt.Println(strings.Repeat("=", 60))

	if initial := strings.TrimSpace(*request); initial != "" {
		fmt.Printf("You: %s\n", initial)
		if err := runModelTurn(ctx, r, sessionID, initial); err != nil {
			return err
		}
		fmt.Println()
	}
	return startModelChat(ctx, reader, r, sessionID)
}

func newSandboxCoordinator(
	reader *bufio.Reader,
	out io.Writer,
	backends ...codeexecutor.SandboxBackend,
) *codeexecutor.SandboxCoordinator {
	opts := []codeexecutor.SandboxCoordinatorOption{
		codeexecutor.WithSandboxPolicyResolver(
			codeexecutor.StaticSandboxPolicyResolver{
				Policy: codeexecutor.ExecutionPolicy{
					Intent: codeexecutor.ExecutionIntentWorkspaceExec,
					FileSystem: codeexecutor.FileSystemPolicy{
						Mode: codeexecutor.FileSystemAccessWorkspaceWrite,
					},
					Network: codeexecutor.NetworkPolicy{
						Mode: codeexecutor.NetworkAccessNone,
					},
					Environment: codeexecutor.EnvironmentPolicy{
						Inheritance: codeexecutor.EnvironmentInheritanceMinimal,
					},
					Approval: codeexecutor.ApprovalPolicy{
						DefaultAction: codeexecutor.ApprovalActionPrompt,
					},
				},
			},
		),
		codeexecutor.WithSandboxApprovalDecider(
			&consoleApprovalDecider{
				in:  reader,
				out: out,
			},
		),
		codeexecutor.WithSandboxBackendSelector(
			&loggingSelector{out: out},
		),
	}
	if len(backends) > 0 {
		opts = append(opts, codeexecutor.WithSandboxBackends(backends...))
	}
	return codeexecutor.NewSandboxCoordinator(opts...)
}

func runDeniedExample(
	ctx context.Context,
	coordinator *codeexecutor.SandboxCoordinator,
	ws codeexecutor.Workspace,
	timeout time.Duration,
) error {
	fmt.Println("[step 1] Try a disallowed interactive command")
	_, err := coordinator.StartProgram(
		ctx,
		codeexecutor.SandboxStartProgramRequest{
			Intent:    codeexecutor.ExecutionIntentWorkspaceExec,
			Workspace: ws,
			Spec: codeexecutor.InteractiveProgramSpec{
				RunProgramSpec: codeexecutor.RunProgramSpec{
					Cmd:     "python3",
					Args:    []string{"-i"},
					Cwd:     codeexecutor.DirWork,
					Timeout: timeout,
				},
			},
			Metadata: map[string]string{
				"example": "sandboxinteractive",
				"step":    "denied",
			},
		},
	)
	if errors.Is(err, codeexecutor.ErrSandboxApprovalDenied) {
		fmt.Println("[result] Approval denied as expected for non-shell command.")
		return nil
	}
	if err == nil {
		return errors.New("expected approval denial, got nil")
	}
	return err
}

func runApprovedDirectExample(
	ctx context.Context,
	reader *bufio.Reader,
	coordinator *codeexecutor.SandboxCoordinator,
	ws codeexecutor.Workspace,
	timeout time.Duration,
) error {
	fmt.Println("[step 2] Start an approved interactive shell session")
	session, err := coordinator.StartProgram(
		ctx,
		codeexecutor.SandboxStartProgramRequest{
			Intent:    codeexecutor.ExecutionIntentWorkspaceExec,
			Workspace: ws,
			Spec: codeexecutor.InteractiveProgramSpec{
				RunProgramSpec: codeexecutor.RunProgramSpec{
					Cmd: "sh",
					Args: []string{
						"-lc",
						"printf 'name: '; read name; " +
							"echo hello:$name; " +
							"echo workspace:$WORKSPACE_DIR",
					},
					Cwd:     codeexecutor.DirWork,
					Timeout: timeout,
				},
				TTY: runtime.GOOS != "windows",
			},
			Metadata: map[string]string{
				"example": "sandboxinteractive",
				"step":    "approved",
			},
		},
	)
	if err != nil {
		return err
	}
	defer session.Close()

	if err := printUntilContains(session, "name: ", 3*time.Second); err != nil {
		return err
	}

	fmt.Print("[session input] enter your name: ")
	name, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "sandbox-user"
	}
	if err := session.Write(name, true); err != nil {
		return err
	}

	if err := printUntilExit(session, 3*time.Second); err != nil {
		return err
	}
	if provider, ok := session.(codeexecutor.ProgramResultProvider); ok {
		result := provider.RunResult()
		fmt.Printf("[final stdout]\n%s", result.Stdout)
		if result.Stderr != "" {
			fmt.Printf("\n[final stderr]\n%s", result.Stderr)
		}
		fmt.Printf("\n[exit code] %d\n", result.ExitCode)
	}
	return nil
}

type consoleApprovalDecider struct {
	in  *bufio.Reader
	out io.Writer
}

func (d *consoleApprovalDecider) DecideSandboxApproval(
	_ context.Context,
	req codeexecutor.ApprovalRequest,
) (codeexecutor.ApprovalResult, error) {
	if strings.TrimSpace(req.Spec.Cmd) != "sh" {
		fmt.Fprintf(
			d.out,
			"[approval] deny %q: this demo only approves interactive shell sessions\n",
			req.Spec.Cmd,
		)
		return codeexecutor.ApprovalResult{
			Action: codeexecutor.ApprovalActionDeny,
			Rule:   "demo_allow_shell_only",
			Reason: "only interactive shell sessions are approved in this demo",
		}, nil
	}

	fmt.Fprintln(d.out, "[approval] interactive request")
	fmt.Fprintf(d.out, "  intent: %s\n", req.Policy.Intent)
	fmt.Fprintf(d.out, "  cmd: %s %s\n", req.Spec.Cmd, strings.Join(req.Spec.Args, " "))
	fmt.Fprintf(d.out, "  cwd: %s\n", req.Spec.Cwd)
	fmt.Fprintf(d.out, "  network: %s\n", req.Policy.Network.Mode)
	fmt.Fprintf(d.out, "  fs mode: %s\n", req.Policy.FileSystem.Mode)
	fmt.Fprint(d.out, "Allow this interactive session? [y/N]: ")

	answer, err := d.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return codeexecutor.ApprovalResult{}, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "y" || answer == "yes" {
		return codeexecutor.ApprovalResult{
			Action: codeexecutor.ApprovalActionAllow,
			Rule:   "console_yes",
			Reason: "approved by console operator",
		}, nil
	}
	return codeexecutor.ApprovalResult{
		Action: codeexecutor.ApprovalActionDeny,
		Rule:   "console_no",
		Reason: "denied by console operator",
	}, nil
}

type loggingSelector struct {
	out io.Writer
}

func (s *loggingSelector) SelectSandboxBackend(
	ctx context.Context,
	req codeexecutor.SandboxRequest,
	backends []codeexecutor.SandboxBackend,
) (codeexecutor.SandboxBackend, error) {
	backend, err := codeexecutor.FirstCompatibleSandboxBackendSelector{}.
		SelectSandboxBackend(ctx, req, backends)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(
		s.out,
		"[backend] selected %s for RunProgram intent=%s cmd=%s\n",
		backend.Name(),
		req.Policy.Intent,
		req.Spec.Cmd,
	)
	return backend, nil
}

func (s *loggingSelector) SelectSandboxInteractiveBackend(
	ctx context.Context,
	req codeexecutor.SandboxInteractiveRequest,
	backends []codeexecutor.SandboxBackend,
) (codeexecutor.SandboxInteractiveBackend, error) {
	backend, err := codeexecutor.FirstCompatibleSandboxBackendSelector{}.
		SelectSandboxInteractiveBackend(ctx, req, backends)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(
		s.out,
		"[backend] selected %s for StartProgram intent=%s tty=%t cmd=%s\n",
		backend.Name(),
		req.Policy.Intent,
		req.Spec.TTY,
		req.Spec.Cmd,
	)
	return backend, nil
}

func printUntilContains(
	session codeexecutor.ProgramSession,
	want string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		poll := session.Poll(nil)
		if poll.Output != "" {
			fmt.Print(poll.Output)
			seen.WriteString(poll.Output)
		}
		if strings.Contains(seen.String(), want) {
			return nil
		}
		if poll.Status == codeexecutor.ProgramStatusExited {
			return fmt.Errorf("session exited before emitting %q", want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q", want)
}

func printUntilExit(
	session codeexecutor.ProgramSession,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		poll := session.Poll(nil)
		if poll.Output != "" {
			fmt.Print(poll.Output)
		}
		if poll.Status == codeexecutor.ProgramStatusExited {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("timed out waiting for interactive session to exit")
}

func printModelRun(eventChan <-chan *event.Event) error {
	fmt.Print("Assistant: ")
	var printed bool
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\nError: %s\n", ev.Error.Message)
			continue
		}
		if err := printToolCalls(ev); err != nil {
			return err
		}
		if len(ev.Response.Choices) == 0 {
			continue
		}
		choice := ev.Response.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
			printed = true
			continue
		}
		if choice.Message.Content != "" && !ev.Done {
			fmt.Print(choice.Message.Content)
			printed = true
		}
	}
	if printed {
		fmt.Println()
	}
	return nil
}

func startModelChat(
	ctx context.Context,
	reader *bufio.Reader,
	r runner.Runner,
	sessionID string,
) error {
	for {
		fmt.Print("You: ")
		text, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			if errors.Is(err, io.EOF) {
				return nil
			}
			continue
		}
		switch strings.ToLower(text) {
		case "/exit":
			return nil
		case "/demo":
			text = sampleModelRequest
			fmt.Println("[using built-in demo prompt]")
		}
		if err := runModelTurn(ctx, r, sessionID, text); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func runModelTurn(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	userText string,
) error {
	evCh, err := r.Run(
		ctx,
		"user",
		sessionID,
		model.NewUserMessage(userText),
	)
	if err != nil {
		return err
	}
	return printModelRun(evCh)
}

func printToolCalls(ev *event.Event) error {
	if len(ev.Response.Choices) == 0 {
		return nil
	}
	msg := ev.Response.Choices[0].Message
	if len(msg.ToolCalls) == 0 {
		return nil
	}
	fmt.Println()
	for _, tc := range msg.ToolCalls {
		fmt.Printf("Tool: %s\n", tc.Function.Name)
		fmt.Printf("Args: %s\n", tc.Function.Arguments)
	}
	fmt.Print("Assistant: ")
	return nil
}

func intPtr(v int) *int {
	return &v
}

const modelDemoInstruction = `You are a demo assistant for sandboxed
interactive workspace execution.

When the user asks for an interactive shell demonstration:
- You MUST use workspace_exec.
- Start the command with background=true and tty=true.
- If the user provides follow-up input in the request, continue the
  same session with workspace_write_stdin.
- When the session output is ready, summarize what happened.

Do not pretend you executed anything without calling the tools.`
