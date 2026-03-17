//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs an OpenClaw runtime and consumes it via A2A.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/app"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultQuestion = "What's the weather in Shanghai today?"
	defaultFollowUp = "What about tomorrow?"

	defaultSkillsRoot  = "./skills"
	defaultA2ABase     = "/a2a"
	defaultA2AName     = "openclaw-sandbox"
	defaultA2ADesc     = "Sandbox agent for bundled skills."
	defaultInstruction = "For live weather or forecast questions, " +
		"call skill_load for the weather skill before skill_run. " +
		"Use the loaded weather skill instead of guessing current " +
		"conditions."
	weatherSkillName  = "weather"
	skillsLoadSession = "session"
	openAIMode        = "openai"

	exampleRunnerName = "openclaw-a2a-example"
	exampleAppName    = "openclaw-a2a-example"
	exampleUserID     = "example-user"
	exampleSessionID  = "example-session"

	startupTimeout = 15 * time.Second
	requestTimeout = 150 * time.Second
	shutdownWait   = 5 * time.Second
	pollInterval   = 100 * time.Millisecond
)

var (
	addrFlag = flag.String(
		"addr",
		"",
		"HTTP listen address for OpenClaw (default random loopback port)",
	)
	skillsRootFlag = flag.String(
		"skills-root",
		defaultSkillsRoot,
		"Path to the OpenClaw skills root",
	)
	stateDirFlag = flag.String(
		"state-dir",
		"",
		"State dir for the embedded OpenClaw runtime",
	)
	modelFlag = flag.String(
		"model",
		"",
		"Optional OpenAI model override",
	)
	baseURLFlag = flag.String(
		"openai-base-url",
		"",
		"Optional OpenAI base URL override",
	)
	questionFlag = flag.String(
		"question",
		defaultQuestion,
		"First user question sent through A2A",
	)
	followUpFlag = flag.String(
		"follow-up",
		defaultFollowUp,
		"Optional follow-up question using the same session",
	)
	streamingFlag = flag.Bool(
		"streaming",
		true,
		"Enable streaming on the OpenClaw A2A surface",
	)
	advertiseToolsFlag = flag.Bool(
		"advertise-tools",
		false,
		"Advertise individual tools in the agent card",
	)
)

func main() {
	log.SetFlags(0)
	flag.Parse()

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	listener, addr, err := listenLoopback(*addrFlag)
	if err != nil {
		return err
	}

	a2aURL := "http://" + addr + defaultA2ABase

	stateDir, cleanupStateDir, err := resolveStateDir(*stateDirFlag)
	if err != nil {
		_ = listener.Close()
		return err
	}
	if cleanupStateDir != nil {
		defer cleanupStateDir()
	}

	configPath, cleanupConfig, err := writeConfigStub()
	if err != nil {
		_ = listener.Close()
		return err
	}
	defer cleanupConfig()

	rt, err := newRuntime(configPath, a2aURL, addr, stateDir)
	if err != nil {
		_ = listener.Close()
		return err
	}
	defer func() {
		_ = rt.Close()
	}()

	httpSrv := newHTTPServer(rt)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpSrv.Serve(listener)
	}()
	defer shutdownServer(httpSrv)

	if err := waitForReady(
		context.Background(),
		addr,
		rt.A2A.AgentCardPath,
	); err != nil {
		return err
	}

	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(a2aURL),
	)
	if err != nil {
		return fmt.Errorf("create a2a agent failed: %w", err)
	}

	card := a2aAgent.GetAgentCard()
	if card == nil {
		return errors.New("resolved a2a agent card is nil")
	}

	fmt.Printf("A2A URL: %s\n", a2aURL)
	fmt.Printf("Agent: %s\n", card.Name)
	fmt.Printf("Published skills: %d\n\n", len(card.Skills))

	sessionSvc := inmemory.NewSessionService()
	procRunner := runner.NewRunner(
		exampleRunnerName,
		a2aAgent,
		runner.WithSessionService(sessionSvc),
	)
	defer procRunner.Close()

	promptList := prompts()
	for idx, prompt := range promptList {
		answer, err := ask(procRunner, prompt)
		if err != nil {
			return err
		}
		fmt.Printf("Q%d: %s\n", idx+1, prompt)
		fmt.Printf("A%d: %s\n\n", idx+1, answer)
	}

	if err := receiveServeErr(serveErr); err != nil {
		return err
	}
	return nil
}

func newRuntime(
	configPath string,
	a2aURL string,
	addr string,
	stateDir string,
) (*app.Runtime, error) {
	args := []string{
		"-config", configPath,
		"-mode", openAIMode,
		"-http-addr", addr,
		"-admin-enabled=false",
		"-skills-root", *skillsRootFlag,
		"-skills-allow-bundled", weatherSkillName,
		"-skills-load-mode", skillsLoadSession,
		"-agent-instruction", defaultInstruction,
		"-state-dir", stateDir,
		"-a2a",
		"-a2a-host", a2aURL,
		fmt.Sprintf("-a2a-streaming=%t", *streamingFlag),
		fmt.Sprintf(
			"-a2a-advertise-tools=%t",
			*advertiseToolsFlag,
		),
		"-a2a-name", defaultA2AName,
		"-a2a-description", defaultA2ADesc,
	}
	if trimmedModel := strings.TrimSpace(*modelFlag); trimmedModel != "" {
		args = append(args, "-model", trimmedModel)
	}
	if trimmedBaseURL := strings.TrimSpace(*baseURLFlag); trimmedBaseURL != "" {
		args = append(args, "-openai-base-url", trimmedBaseURL)
	}

	rt, err := app.NewRuntime(context.Background(), args)
	if err != nil {
		return nil, fmt.Errorf("create openclaw runtime failed: %w", err)
	}
	return rt, nil
}

func newHTTPServer(rt *app.Runtime) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/", rt.Gateway.Handler)
	mux.Handle(rt.A2A.BasePath, rt.A2A.Handler)
	mux.Handle(rt.A2A.BasePath+"/", rt.A2A.Handler)

	return &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: shutdownWait,
	}
}

func listenLoopback(rawAddr string) (net.Listener, string, error) {
	addr := strings.TrimSpace(rawAddr)
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("listen on %s failed: %w", addr, err)
	}
	return listener, listener.Addr().String(), nil
}

func resolveStateDir(raw string) (string, func(), error) {
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		return trimmed, nil, nil
	}

	stateDir, err := os.MkdirTemp("", "openclaw-a2a-example-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp state dir failed: %w", err)
	}
	return stateDir, func() {
		_ = os.RemoveAll(stateDir)
	}, nil
}

func writeConfigStub() (string, func(), error) {
	configFile, err := os.CreateTemp(
		"",
		"openclaw-a2a-example-*.yaml",
	)
	if err != nil {
		return "", nil, fmt.Errorf("create temp config failed: %w", err)
	}

	content := []byte("app_name: \"" + exampleAppName + "\"\n")
	if _, err := configFile.Write(content); err != nil {
		_ = configFile.Close()
		_ = os.Remove(configFile.Name())
		return "", nil, fmt.Errorf("write temp config failed: %w", err)
	}
	if err := configFile.Close(); err != nil {
		_ = os.Remove(configFile.Name())
		return "", nil, fmt.Errorf("close temp config failed: %w", err)
	}

	return configFile.Name(), func() {
		_ = os.Remove(configFile.Name())
	}, nil
}

func waitForReady(
	ctx context.Context,
	addr string,
	cardPath string,
) error {
	deadline := time.Now().Add(startupTimeout)
	url := "http://" + addr + cardPath
	client := &http.Client{
		Timeout: pollInterval,
	}

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			url,
			nil,
		)
		if err != nil {
			return fmt.Errorf("build readiness request failed: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		} else if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("a2a card was not ready at %s", url)
}

func prompts() []string {
	items := []string{
		strings.TrimSpace(*questionFlag),
	}
	if followUp := strings.TrimSpace(*followUpFlag); followUp != "" {
		items = append(items, followUp)
	}
	return items
}

func ask(procRunner runner.Runner, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	eventCh, err := procRunner.Run(
		ctx,
		exampleUserID,
		exampleSessionID,
		model.NewUserMessage(prompt),
		agent.WithStream(*streamingFlag),
	)
	if err != nil {
		return "", fmt.Errorf("run a2a request failed: %w", err)
	}

	answer, err := collectAnswer(eventCh)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return "", errors.New("assistant returned an empty answer")
	}
	return answer, nil
}

func collectAnswer(eventCh <-chan *event.Event) (string, error) {
	var (
		finalText strings.Builder
		streamed  strings.Builder
	)

	for evt := range eventCh {
		if evt.Error != nil {
			return "", errors.New(evt.Error.Message)
		}
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Delta.Content != "" {
				streamed.WriteString(choice.Delta.Content)
			}
			if choice.Message.Content != "" {
				finalText.Reset()
				finalText.WriteString(choice.Message.Content)
			}
		}
	}

	if text := strings.TrimSpace(finalText.String()); text != "" {
		return text, nil
	}
	return strings.TrimSpace(streamed.String()), nil
}

func shutdownServer(httpSrv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

func receiveServeErr(serveErr <-chan error) error {
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server failed: %w", err)
		}
	default:
	}
	return nil
}
