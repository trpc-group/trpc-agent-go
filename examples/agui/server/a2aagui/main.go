//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates serving A2A Protocol v1.0 and AG-UI from one
// process and one HTTP port. Both protocol servers share one local agent and
// one core runner. A recording adapter persists A2A runs as AG-UI tracks so
// the AG-UI history endpoint can replay sessions created through either API.
//
// Routes:
//
//	/a2a/              A2A endpoint
//	/a2a/.well-known/agent-card.json
//	/ui/chat            AG-UI chat endpoint
//	/ui/history         AG-UI message snapshot endpoint
//	/api/sessions       session list endpoint
//
// Run from the examples/agui module:
//
//	go run ./server/a2aagui -model deepseek-v4-flash
//
// AG-UI chat and history requests, and session-list requests, may carry an
// X-User-ID header. This example defaults to "demo-user" when it is absent.
// Production services should derive this identity from authenticated request
// context instead of trusting a caller-provided header directly.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String(
		"model",
		getEnvOrDefault("MODEL_NAME", "deepseek-v4-flash"),
		"LLM model name",
	)
	listenAddr = flag.String(
		"addr",
		"127.0.0.1:8080",
		"HTTP listen address",
	)
	publicURL = flag.String(
		"public-url",
		"http://localhost:8080",
		"public base URL used in the A2A Agent Card; keep its port in sync with -addr",
	)
	enableStream = flag.Bool("stream", true, "enable streaming")
)

const (
	agentVersion       = "1.0.0"
	uiAppName          = "a2a-agui-demo"
	a2aBasePath        = "/a2a"
	uiBasePath         = "/ui"
	uiChatPath         = "/chat"
	uiHistoryPath      = "/history"
	sessionListPath    = "/api/sessions"
	userIDHeader       = "X-User-ID"
	defaultUserID      = "demo-user"
	defaultPageSize    = 20
	maxSessionPageSize = 100
)

type userIDContextKey struct{}

type sessionListItem struct {
	ThreadID  string    `json:"threadId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type sessionListResponse struct {
	Sessions []sessionListItem `json:"sessions"`
	Offset   int               `json:"offset"`
	Limit    int               `json:"limit"`
	HasMore  bool              `json:"hasMore"`
}

func main() {
	flag.Parse()

	// In-memory storage keeps the example self-contained. Production deployments
	// should use one shared persistent session.TrackService instead.
	sessionService := inmemory.NewSessionService()
	defer func() {
		if err := sessionService.Close(); err != nil {
			log.Errorf("close session service: %v", err)
		}
	}()

	localAgent := buildLocalAgent()
	a2aURL := strings.TrimRight(*publicURL, "/") + a2aBasePath + "/"
	agentCard, err := a2aserver.NewAgentCard(
		localAgent.Info().Name,
		localAgent.Info().Description,
		agentVersion,
		a2aURL,
		*enableStream,
		a2aserver.WithCardTools(localAgent.Tools()...),
	)
	if err != nil {
		log.Fatalf("create A2A Agent Card: %v", err)
	}

	sharedRunner := runner.NewRunner(
		uiAppName,
		localAgent,
		runner.WithSessionService(sessionService),
	)
	a2aRunner, err := agui.NewRecordingRunner(
		sharedRunner,
		uiAppName,
		sessionService,
	)
	if err != nil {
		log.Fatalf("create A2A recording runner: %v", err)
	}
	defer func() {
		if err := a2aRunner.Close(); err != nil {
			log.Errorf("close shared runner: %v", err)
		}
	}()

	uiServer, err := agui.New(
		sharedRunner,
		agui.WithBasePath(uiBasePath),
		agui.WithPath(uiChatPath),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath(uiHistoryPath),
		agui.WithAppName(uiAppName),
		agui.WithSessionService(sessionService),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithUserIDResolver(userIDResolver),
		),
	)
	if err != nil {
		log.Fatalf("create AG-UI server: %v", err)
	}

	a2aServer, err := a2aserver.New(
		a2aserver.WithAgentCard(agentCard),
		a2aserver.WithRunner(a2aRunner),
	)
	if err != nil {
		log.Fatalf("create A2A server: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle(uiBasePath+"/", userIDMiddleware(uiServer.Handler()))
	mux.Handle(
		sessionListPath,
		userIDMiddleware(newSessionListHandler(sessionService, uiAppName)),
	)
	mux.Handle("/", userIDMiddleware(a2aServer.Handler()))

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}

	log.Infof("combined server listening on %s", *listenAddr)
	log.Infof("A2A Agent Card: %s.well-known/agent-card.json", a2aURL)
	log.Infof("AG-UI chat: %s%s%s", strings.TrimRight(*publicURL, "/"), uiBasePath, uiChatPath)
	log.Infof("AG-UI history: %s%s%s", strings.TrimRight(*publicURL, "/"), uiBasePath, uiHistoryPath)
	log.Infof("session list: %s%s", strings.TrimRight(*publicURL, "/"), sessionListPath)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- httpServer.ListenAndServe()
	}()

	signalCtx, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("combined server stopped: %v", err)
		}
		return
	case <-signalCtx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Errorf("stop combined server: %v", err)
	}
	if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Errorf("combined server stopped: %v", err)
	}
}

func buildLocalAgent() *llmagent.LLMAgent {
	modelInstance := openai.New(*modelName)
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription(
			"A calculator tool. Parameters: a (first number), b (second number), "+
				"operation (add, subtract, multiply, divide, power).",
		),
	)
	return llmagent.New(
		"calculator-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An assistant with calculator capabilities"),
		llmagent.WithInstruction(
			"You are a helpful assistant with a calculator tool. Use it when asked to compute.",
		),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1024),
			Temperature: floatPtr(0.7),
			Stream:      *enableStream,
		}),
	)
}

// userIDMiddleware demonstrates identity propagation for both protocols.
// Production code should replace it with authenticated middleware.
func userIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get(userIDHeader))
		if userID == "" {
			userID = defaultUserID
		}
		r.Header.Set(userIDHeader, userID)
		ctx := context.WithValue(r.Context(), userIDContextKey{}, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userIDResolver(ctx context.Context, _ *adapter.RunAgentInput) (string, error) {
	return userIDFromContext(ctx), nil
}

func userIDFromContext(ctx context.Context) string {
	if userID, ok := ctx.Value(userIDContextKey{}).(string); ok && userID != "" {
		return userID
	}
	return defaultUserID
}

func newSessionListHandler(service session.Service, appName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSessionListCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		offset, limit, err := sessionListPage(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sessions, err := service.ListSessions(
			r.Context(),
			session.UserKey{
				AppName: appName,
				UserID:  userIDFromContext(r.Context()),
			},
			session.WithListSessionOnlyMeta(),
			session.WithListSessionPage(offset, limit+1),
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("list sessions: %v", err), http.StatusInternalServerError)
			return
		}

		hasMore := len(sessions) > limit
		if hasMore {
			sessions = sessions[:limit]
		}
		items := make([]sessionListItem, 0, len(sessions))
		for _, sess := range sessions {
			if sess == nil {
				continue
			}
			items = append(items, sessionListItem{
				ThreadID:  sess.ID,
				CreatedAt: sess.CreatedAt,
				UpdatedAt: sess.UpdatedAt,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(sessionListResponse{
			Sessions: items,
			Offset:   offset,
			Limit:    limit,
			HasMore:  hasMore,
		}); err != nil {
			log.ErrorfContext(r.Context(), "encode session list: %v", err)
		}
	})
}

func setSessionListCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", http.MethodGet)
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+userIDHeader)
}

func sessionListPage(r *http.Request) (int, int, error) {
	offset, err := queryInt(r, "offset", 0)
	if err != nil || offset < 0 {
		return 0, 0, fmt.Errorf("offset must be a non-negative integer")
	}
	limit, err := queryInt(r, "limit", defaultPageSize)
	if err != nil || limit <= 0 || limit > maxSessionPageSize {
		return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxSessionPageSize)
	}
	return offset, limit, nil
}

func queryInt(r *http.Request, name string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return defaultValue, nil
	}
	return strconv.Atoi(raw)
}

func calculator(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
func getEnvOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}
