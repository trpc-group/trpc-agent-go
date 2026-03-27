//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a simple per-session persona example.
//
// Each session stores its own persona in session state. Before every
// runner.Run call, the demo loads that persona and passes it through
// agent.WithGlobalInstruction(...), so the active system prompt is decided
// dynamically for that run.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
)

const (
	appName              = "session-persona-demo"
	agentName            = "persona-assistant"
	defaultUserID        = "user"
	personaStateKey      = "assistant_persona"
	defaultModelName     = "deepseek-chat"
	defaultSessionType   = "inmemory"
	defaultEventLimit    = 1000
	defaultBannerWidth   = 72
	defaultPreviewMax    = 96
	escapedNewline       = "\\n"
	actualNewline        = "\n"
	personaSessionPrefix = "persona-session"

	commandExit        = "/exit"
	commandPersona     = "/persona"
	commandShowPersona = "/show-persona"
	commandSessions    = "/sessions"
	commandNew         = "/new"
	commandUse         = "/use"

	defaultSessionTTL = 24 * time.Hour

	defaultPersona = "You are a practical Go mentor for this session. " +
		"Prefer concise answers, explain trade-offs, and keep examples " +
		"compact."
	instructionText = "Answer the latest user request directly. Follow the " +
		"active session persona for tone, expertise, and response style."
	setPersonaUsage = "Usage: /persona <text>"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the model to use (default: MODEL_NAME env var or "+
			"deepseek-chat)",
	)
	sessionType = flag.String(
		"session",
		defaultSessionType,
		"Session backend: inmemory / sqlite / redis / postgres / mysql / "+
			"clickhouse",
	)
	eventLimit = flag.Int(
		"event-limit",
		defaultEventLimit,
		"Maximum number of events to store per session",
	)
	sessionTTL = flag.Duration(
		"session-ttl",
		defaultSessionTTL,
		"Session time-to-live duration",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode",
	)
)

type personaDemo struct {
	modelName      string
	sessionType    string
	eventLimit     int
	sessionTTL     time.Duration
	streaming      bool
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

func main() {
	flag.Parse()

	demo := &personaDemo{
		modelName:   getModelName(),
		sessionType: *sessionType,
		eventLimit:  *eventLimit,
		sessionTTL:  *sessionTTL,
		streaming:   *streaming,
	}
	if err := demo.run(); err != nil {
		log.Fatalf("Session persona demo failed: %v", err)
	}
}

func getModelName() string {
	if *modelName != "" {
		return *modelName
	}
	return defaultModelName
}

func validateSessionType(value string) (util.SessionType, error) {
	normalized := strings.TrimSpace(strings.ToLower(value))
	supportedTypes := map[string]util.SessionType{
		string(util.SessionInMemory):   util.SessionInMemory,
		string(util.SessionSQLite):     util.SessionSQLite,
		string(util.SessionRedis):      util.SessionRedis,
		string(util.SessionPostgres):   util.SessionPostgres,
		string(util.SessionMySQL):      util.SessionMySQL,
		string(util.SessionClickHouse): util.SessionClickHouse,
	}
	sessionType, ok := supportedTypes[normalized]
	if ok {
		return sessionType, nil
	}

	allowedTypes := make([]string, 0, len(supportedTypes))
	for name := range supportedTypes {
		allowedTypes = append(allowedTypes, name)
	}
	sort.Strings(allowedTypes)
	return "", fmt.Errorf(
		"unsupported session backend %q, expected one of: %s",
		value,
		strings.Join(allowedTypes, ", "),
	)
}

func (d *personaDemo) run() error {
	ctx := context.Background()
	if err := d.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer d.runner.Close()

	d.printIntro(ctx)
	return d.startChat(ctx)
}

func (d *personaDemo) setup(ctx context.Context) error {
	validatedSessionType, err := validateSessionType(d.sessionType)
	if err != nil {
		return err
	}
	d.sessionType = string(validatedSessionType)

	sessionService, err := util.NewSessionServiceByType(
		validatedSessionType,
		util.SessionServiceConfig{
			EventLimit: d.eventLimit,
			TTL:        d.sessionTTL,
		},
	)
	if err != nil {
		return fmt.Errorf("create session service failed: %w", err)
	}
	d.sessionService = sessionService
	d.userID = defaultUserID
	d.sessionID = newSessionID()

	if err := d.ensureSession(ctx, d.sessionID); err != nil {
		return err
	}

	agt := llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(d.modelName)),
		llmagent.WithDescription(
			"Assistant demo with per-run session persona override.",
		),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: d.streaming,
		}),
	)

	d.runner = runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(d.sessionService),
	)
	return nil
}

func (d *personaDemo) printIntro(ctx context.Context) {
	fmt.Println("Session Persona Demo")
	fmt.Printf("Model: %s\n", d.modelName)
	fmt.Printf("Session backend: %s\n", d.sessionType)
	fmt.Printf("Streaming: %t\n", d.streaming)
	fmt.Printf("Active session: %s\n", d.sessionID)
	fmt.Println(strings.Repeat("=", defaultBannerWidth))
	fmt.Println("Commands:")
	fmt.Println("  /persona <text>   - Set the current session persona")
	fmt.Println("  /show-persona     - Show the current session persona")
	fmt.Println("  /new [id]         - Start a new session with default persona")
	fmt.Println("  /use <id>         - Switch to another session")
	fmt.Println("  /sessions         - List sessions and their persona previews")
	fmt.Println("  /exit             - End the demo")
	fmt.Println()
	fmt.Println("Tip:")
	fmt.Println("  Use \\n inside /persona to store a multi-line persona.")
	fmt.Println()
	fmt.Println("Example flow:")
	fmt.Println("  1. Ask a question in the first session")
	fmt.Println("  2. /persona You are a strict code reviewer.")
	fmt.Println("  3. Continue chatting in the same session")
	fmt.Println("  4. /new, set another persona, then /use to switch back")
	fmt.Println()
	d.showPersona(ctx)
	fmt.Println()
}

func (d *personaDemo) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		handled, shouldExit, err := d.handleCommand(ctx, userInput)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}
		if shouldExit {
			fmt.Println("Goodbye!")
			return nil
		}
		if handled {
			fmt.Println()
			continue
		}

		if err := d.processMessage(ctx, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (d *personaDemo) handleCommand(
	ctx context.Context,
	userInput string,
) (bool, bool, error) {
	lowerInput := strings.ToLower(userInput)

	switch {
	case lowerInput == commandExit:
		return true, true, nil
	case lowerInput == commandShowPersona:
		d.showPersona(ctx)
		return true, false, nil
	case lowerInput == commandSessions:
		return true, false, d.listSessions(ctx)
	case hasCommandPrefix(lowerInput, commandPersona):
		persona := normalizeInput(commandArgument(userInput))
		if persona == "" {
			fmt.Println(setPersonaUsage)
			return true, false, nil
		}
		return true, false, d.setPersona(ctx, persona)
	case lowerInput == commandPersona:
		fmt.Println(setPersonaUsage)
		return true, false, nil
	case lowerInput == commandNew || hasCommandPrefix(lowerInput, commandNew):
		targetSessionID := commandArgument(userInput)
		if targetSessionID == "" {
			targetSessionID = newSessionID()
		}
		return true, false, d.switchSession(ctx, targetSessionID, true)
	case hasCommandPrefix(lowerInput, commandUse):
		targetSessionID := commandArgument(userInput)
		if targetSessionID == "" {
			fmt.Println("Usage: /use <session-id>")
			return true, false, nil
		}
		return true, false, d.switchSession(ctx, targetSessionID, false)
	case lowerInput == commandUse:
		fmt.Println("Usage: /use <session-id>")
		return true, false, nil
	}
	return false, false, nil
}

func hasCommandPrefix(input, command string) bool {
	return strings.HasPrefix(input, command+" ")
}

func commandArgument(input string) string {
	_, arg, ok := strings.Cut(input, " ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(arg)
}

func normalizeInput(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, escapedNewline, actualNewline)
	return strings.TrimSpace(value)
}

func (d *personaDemo) processMessage(
	ctx context.Context,
	userInput string,
) error {
	persona, err := d.currentPersona(ctx)
	if err != nil {
		return err
	}

	eventChan, err := d.runner.Run(
		ctx,
		d.userID,
		d.sessionID,
		model.NewUserMessage(userInput),
		agent.WithGlobalInstruction(buildPersonaInstruction(persona)),
	)
	if err != nil {
		return fmt.Errorf("run agent failed: %w", err)
	}
	return d.processResponse(eventChan)
}

func buildPersonaInstruction(persona string) string {
	persona = strings.TrimSpace(persona)
	if persona == "" {
		persona = defaultPersona
	}
	return "You are the assistant for the current session. The session " +
		"persona below is authoritative. Adapt tone, expertise, and answer " +
		"style to it.\nSession persona:\n" + persona
}

func (d *personaDemo) processResponse(
	eventChan <-chan *event.Event,
) error {
	fmt.Print("Assistant: ")
	printed := false

	for ev := range eventChan {
		if ev.Error != nil {
			return fmt.Errorf("model error: %s", ev.Error.Message)
		}
		if len(ev.Choices) > 0 {
			content := d.extractContent(ev.Choices[0])
			if content != "" {
				fmt.Print(content)
				printed = true
			}
		}
		if ev.IsFinalResponse() {
			if !printed {
				fmt.Print("(empty response)")
			}
			fmt.Println()
			return nil
		}
	}
	if !printed {
		fmt.Print("(no final response)")
	}
	fmt.Println()
	return nil
}

func (d *personaDemo) extractContent(choice model.Choice) string {
	if d.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func (d *personaDemo) setPersona(
	ctx context.Context,
	persona string,
) error {
	if persona == "" {
		return fmt.Errorf("persona must not be empty")
	}
	if err := d.ensureSession(ctx, d.sessionID); err != nil {
		return err
	}

	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	}
	if err := d.sessionService.UpdateSessionState(ctx, key, session.StateMap{
		personaStateKey: []byte(persona),
	}); err != nil {
		return fmt.Errorf("update persona failed: %w", err)
	}

	fmt.Printf("Updated persona for session %s.\n", d.sessionID)
	d.showPersona(ctx)
	return nil
}

func (d *personaDemo) showPersona(ctx context.Context) {
	persona, err := d.currentPersona(ctx)
	if err != nil {
		fmt.Printf("Failed to load persona: %v\n", err)
		return
	}
	fmt.Printf("Active session: %s\n", d.sessionID)
	fmt.Println("Persona:")
	fmt.Println(persona)
}

func (d *personaDemo) currentPersona(ctx context.Context) (string, error) {
	sess, err := d.loadSession(ctx, d.sessionID)
	if err != nil {
		return "", err
	}
	return personaFromSession(sess), nil
}

func (d *personaDemo) listSessions(ctx context.Context) error {
	sessions, err := d.sessionService.ListSessions(ctx, session.UserKey{
		AppName: appName,
		UserID:  d.userID,
	})
	if err != nil {
		return fmt.Errorf("list sessions failed: %w", err)
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID < sessions[j].ID
	})

	fmt.Println("Sessions:")
	for _, sess := range sessions {
		marker := " "
		if sess.ID == d.sessionID {
			marker = "*"
		}
		preview := singleLinePersona(personaFromSession(sess))
		preview = util.Truncate(preview, defaultPreviewMax)
		fmt.Printf("  %s %s\n", marker, sess.ID)
		fmt.Printf("    Persona: %s\n", preview)
	}
	return nil
}

func (d *personaDemo) switchSession(
	ctx context.Context,
	targetSessionID string,
	announceNew bool,
) error {
	targetSessionID = strings.TrimSpace(targetSessionID)
	if targetSessionID == "" {
		return fmt.Errorf("session id must not be empty")
	}
	if targetSessionID == d.sessionID {
		fmt.Printf("Already using session %s.\n", d.sessionID)
		d.showPersona(ctx)
		return nil
	}
	if announceNew {
		if err := d.ensureSession(ctx, targetSessionID); err != nil {
			return err
		}
	} else {
		sess, err := d.loadSession(ctx, targetSessionID)
		if err != nil {
			return err
		}
		if sess == nil {
			return fmt.Errorf("session %s does not exist", targetSessionID)
		}
	}

	previousSessionID := d.sessionID
	d.sessionID = targetSessionID
	if announceNew {
		fmt.Printf("Started session %s.\n", d.sessionID)
	} else {
		fmt.Printf("Switched from %s to %s.\n", previousSessionID, d.sessionID)
	}
	d.showPersona(ctx)
	return nil
}

func (d *personaDemo) ensureSession(
	ctx context.Context,
	targetSessionID string,
) error {
	sess, err := d.loadSession(ctx, targetSessionID)
	if err != nil {
		return err
	}
	if sess != nil {
		if _, ok := sess.State[personaStateKey]; ok {
			return nil
		}
		return d.updatePersona(ctx, targetSessionID, defaultPersona)
	}

	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: targetSessionID,
	}
	_, err = d.sessionService.CreateSession(ctx, key, session.StateMap{
		personaStateKey: []byte(defaultPersona),
	})
	if err != nil {
		return fmt.Errorf("create session failed: %w", err)
	}
	return nil
}

func (d *personaDemo) loadSession(
	ctx context.Context,
	targetSessionID string,
) (*session.Session, error) {
	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: targetSessionID,
	}
	sess, err := d.sessionService.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get session failed: %w", err)
	}
	return sess, nil
}

func (d *personaDemo) updatePersona(
	ctx context.Context,
	targetSessionID string,
	persona string,
) error {
	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: targetSessionID,
	}
	if err := d.sessionService.UpdateSessionState(ctx, key, session.StateMap{
		personaStateKey: []byte(persona),
	}); err != nil {
		return fmt.Errorf("initialize persona failed: %w", err)
	}
	return nil
}

func personaFromSession(sess *session.Session) string {
	if sess == nil {
		return "(missing session)"
	}
	persona, ok := sess.State[personaStateKey]
	if !ok || len(persona) == 0 {
		return defaultPersona
	}
	return string(persona)
}

func singleLinePersona(persona string) string {
	persona = strings.ReplaceAll(persona, actualNewline, " ")
	return strings.Join(strings.Fields(persona), " ")
}

func newSessionID() string {
	return fmt.Sprintf(
		"%s-%d",
		personaSessionPrefix,
		time.Now().UnixNano(),
	)
}
