//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides utility functions for session examples.
package util

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sessionpgvector "trpc.group/trpc-go/trpc-agent-go/session/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/session/postgres"
	"trpc.group/trpc-go/trpc-agent-go/session/redis"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SessionType defines the type of session service.
type SessionType string

// Session type constants.
const (
	SessionInMemory   SessionType = "inmemory"
	SessionSQLite     SessionType = "sqlite"
	SessionRedis      SessionType = "redis"
	SessionPostgres   SessionType = "postgres"
	SessionPGVector   SessionType = "pgvector"
	SessionMySQL      SessionType = "mysql"
	SessionClickHouse SessionType = "clickhouse"
)

// SessionServiceConfig holds configuration for creating a session service.
type SessionServiceConfig struct {
	EventLimit       int
	TTL              time.Duration
	AppendEventHooks []session.AppendEventHook
	GetSessionHooks  []session.GetSessionHook
	EnableTracing    bool // enable OpenTelemetry tracing (redis only)
}

// NewSessionServiceByType creates a session service based on the specified
// type.
//
// Parameters:
//   - sessionType: one of inmemory, sqlite, redis, postgres, pgvector,
//     mysql, clickhouse
//   - cfg: session service configuration (eventLimit, ttl, hooks)
//
// Environment variables by session type:
//
//	sqlite:     SQLITE_SESSION_DSN (default:
//	  file:sessions.db?_busy_timeout=5000)
//	redis:      REDIS_ADDR (default: localhost:6379)
//	postgres:   PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, PG_DATABASE
//	pgvector:   PGVECTOR_HOST, PGVECTOR_PORT, PGVECTOR_USER,
//	  PGVECTOR_PASSWORD, PGVECTOR_DATABASE, PGVECTOR_EMBEDDER_MODEL
//	mysql:      MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD,
//	  MYSQL_DATABASE
//	clickhouse: CLICKHOUSE_HOST, CLICKHOUSE_PORT, CLICKHOUSE_USER,
//	  CLICKHOUSE_PASSWORD, CLICKHOUSE_DATABASE
func NewSessionServiceByType(
	sessionType SessionType,
	cfg SessionServiceConfig,
) (session.Service, error) {
	switch sessionType {
	case SessionSQLite:
		return newSQLiteSessionService(cfg)
	case SessionRedis:
		return newRedisSessionService(cfg)
	case SessionPostgres:
		return newPostgresSessionService(cfg)
	case SessionPGVector:
		return newPGVectorSessionService(cfg)
	case SessionMySQL:
		return newMySQLSessionService(cfg)
	case SessionClickHouse:
		return newClickHouseSessionService(cfg)
	case SessionInMemory:
		fallthrough
	default:
		return newInMemorySessionService(cfg), nil
	}
}

const (
	sqliteSessionDSNEnvKey    = "SQLITE_SESSION_DSN"
	defaultSQLiteSessionDBDSN = "file:sessions.db?_busy_timeout=5000"
	sqliteDriverName          = "sqlite3"
	defaultSQLiteMaxOpenConns = 1
	defaultSQLiteMaxIdleConns = 1

	openAIEmbeddingAPIKeyEnvKey  = "OPENAI_EMBEDDING_API_KEY"
	openAIEmbeddingBaseURLEnvKey = "OPENAI_EMBEDDING_BASE_URL"
	openAIEmbeddingModelEnvKey   = "OPENAI_EMBEDDING_MODEL"
)

func newSQLiteSessionService(
	cfg SessionServiceConfig,
) (session.Service, error) {
	dsn := GetEnvOrDefault(sqliteSessionDSNEnvKey, defaultSQLiteSessionDBDSN)
	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(defaultSQLiteMaxOpenConns)
	db.SetMaxIdleConns(defaultSQLiteMaxIdleConns)

	svc, err := sessionsqlite.NewService(
		db,
		sessionsqlite.WithSessionEventLimit(cfg.EventLimit),
		sessionsqlite.WithSessionTTL(cfg.TTL),
		sessionsqlite.WithAppendEventHook(cfg.AppendEventHooks...),
		sessionsqlite.WithGetSessionHook(cfg.GetSessionHooks...),
	)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return svc, nil
}

func newInMemorySessionService(cfg SessionServiceConfig) session.Service {
	return sessioninmemory.NewSessionService(
		sessioninmemory.WithSessionEventLimit(cfg.EventLimit),
		sessioninmemory.WithSessionTTL(cfg.TTL),
		sessioninmemory.WithAppendEventHook(cfg.AppendEventHooks...),
		sessioninmemory.WithGetSessionHook(cfg.GetSessionHooks...),
	)
}

// newRedisSessionService creates a Redis session service.
// Environment variables:
//   - REDIS_ADDR: Redis server address (default: localhost:6379)
func newRedisSessionService(
	cfg SessionServiceConfig,
) (session.Service, error) {
	addr := GetEnvOrDefault("REDIS_ADDR", "localhost:6379")
	redisURL := fmt.Sprintf("redis://%s", addr)

	return redis.NewService(
		redis.WithRedisClientURL(redisURL),
		redis.WithSessionEventLimit(cfg.EventLimit),
		redis.WithSessionTTL(cfg.TTL),
		redis.WithAppendEventHook(cfg.AppendEventHooks...),
		redis.WithGetSessionHook(cfg.GetSessionHooks...),
		redis.WithCompatMode(redis.CompatModeLegacy),
		redis.WithEnableTracing(cfg.EnableTracing),
	)
}

// newPostgresSessionService creates a PostgreSQL session service.
// Environment variables:
//   - PG_HOST: PostgreSQL host (default: localhost)
//   - PG_PORT: PostgreSQL port (default: 5432)
//   - PG_USER: PostgreSQL user (default: root)
//   - PG_PASSWORD: PostgreSQL password (default: empty)
//   - PG_DATABASE: PostgreSQL database (default: trpc_agent_go)
func newPostgresSessionService(
	cfg SessionServiceConfig,
) (session.Service, error) {
	host := GetEnvOrDefault("PG_HOST", "localhost")
	portStr := GetEnvOrDefault("PG_PORT", "5432")
	port, _ := strconv.Atoi(portStr)
	user := GetEnvOrDefault("PG_USER", "root")
	password := GetEnvOrDefault("PG_PASSWORD", "")
	database := GetEnvOrDefault("PG_DATABASE", "trpc_agent_go")

	return postgres.NewService(
		postgres.WithHost(host),
		postgres.WithPort(port),
		postgres.WithUser(user),
		postgres.WithPassword(password),
		postgres.WithDatabase(database),
		postgres.WithTablePrefix("trpc_"),
		postgres.WithSessionEventLimit(cfg.EventLimit),
		postgres.WithSessionTTL(cfg.TTL),
		postgres.WithAppendEventHook(cfg.AppendEventHooks...),
		postgres.WithGetSessionHook(cfg.GetSessionHooks...),
	)
}

func getEmbeddingModel(defaultModel string) string {
	if env := os.Getenv(openAIEmbeddingModelEnvKey); env != "" {
		return env
	}
	return defaultModel
}

func newOpenAIEmbedder(defaultModel string) *openaiembedder.Embedder {
	modelName := getEmbeddingModel(defaultModel)
	opts := []openaiembedder.Option{
		openaiembedder.WithModel(modelName),
	}

	if apiKey := os.Getenv(openAIEmbeddingAPIKeyEnvKey); apiKey != "" {
		opts = append(opts, openaiembedder.WithAPIKey(apiKey))
	}

	baseURL := os.Getenv(openAIEmbeddingBaseURLEnvKey)
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL != "" {
		opts = append(opts, openaiembedder.WithBaseURL(baseURL))
	}

	return openaiembedder.New(opts...)
}

// newPGVectorSessionService creates a PostgreSQL + pgvector
// backed session service.
// Environment variables:
//   - PGVECTOR_HOST: PostgreSQL host (default: localhost)
//   - PGVECTOR_PORT: PostgreSQL port (default: 5432)
//   - PGVECTOR_USER: PostgreSQL user (default: postgres)
//   - PGVECTOR_PASSWORD: PostgreSQL password (default: empty)
//   - PGVECTOR_DATABASE: PostgreSQL database (default:
//     trpc-agent-go-pgsession)
//   - PGVECTOR_EMBEDDER_MODEL: Embedder model name (default:
//     text-embedding-3-small)
func newPGVectorSessionService(
	cfg SessionServiceConfig,
) (session.Service, error) {
	host := GetEnvOrDefault("PGVECTOR_HOST", "localhost")
	portStr := GetEnvOrDefault("PGVECTOR_PORT", "5432")
	port := 5432
	if parsed, err := strconv.Atoi(portStr); err == nil {
		port = parsed
	}
	user := GetEnvOrDefault("PGVECTOR_USER", "postgres")
	password := GetEnvOrDefault("PGVECTOR_PASSWORD", "")
	database := GetEnvOrDefault(
		"PGVECTOR_DATABASE", "trpc-agent-go-pgsession",
	)
	embedderModel := GetEnvOrDefault(
		"PGVECTOR_EMBEDDER_MODEL",
		openaiembedder.DefaultModel,
	)
	embedder := newOpenAIEmbedder(embedderModel)

	return sessionpgvector.NewService(
		sessionpgvector.WithHost(host),
		sessionpgvector.WithPort(port),
		sessionpgvector.WithUser(user),
		sessionpgvector.WithPassword(password),
		sessionpgvector.WithDatabase(database),
		sessionpgvector.WithIndexDimension(embedder.GetDimensions()),
		sessionpgvector.WithEmbedder(embedder),
		sessionpgvector.WithTablePrefix("trpc_"),
		sessionpgvector.WithSessionEventLimit(cfg.EventLimit),
		sessionpgvector.WithSessionTTL(cfg.TTL),
		sessionpgvector.WithAppendEventHook(cfg.AppendEventHooks...),
		sessionpgvector.WithGetSessionHook(cfg.GetSessionHooks...),
	)
}

// newMySQLSessionService creates a MySQL session service.
// Environment variables:
//   - MYSQL_HOST: MySQL host (default: localhost)
//   - MYSQL_PORT: MySQL port (default: 3306)
//   - MYSQL_USER: MySQL user (default: root)
//   - MYSQL_PASSWORD: MySQL password (default: empty)
//   - MYSQL_DATABASE: MySQL database (default: trpc_agent_go)
func newMySQLSessionService(
	cfg SessionServiceConfig,
) (session.Service, error) {
	host := GetEnvOrDefault("MYSQL_HOST", "localhost")
	port := GetEnvOrDefault("MYSQL_PORT", "3306")
	user := GetEnvOrDefault("MYSQL_USER", "root")
	password := GetEnvOrDefault("MYSQL_PASSWORD", "")
	database := GetEnvOrDefault("MYSQL_DATABASE", "trpc_agent_go")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		user, password, host, port, database)

	return mysql.NewService(
		mysql.WithMySQLClientDSN(dsn),
		mysql.WithTablePrefix("trpc_"),
		mysql.WithSessionEventLimit(cfg.EventLimit),
		mysql.WithSessionTTL(cfg.TTL),
		mysql.WithAppendEventHook(cfg.AppendEventHooks...),
		mysql.WithGetSessionHook(cfg.GetSessionHooks...),
	)
}

// newClickHouseSessionService creates a ClickHouse session service.
// Environment variables:
//   - CLICKHOUSE_HOST: ClickHouse host (default: localhost)
//   - CLICKHOUSE_PORT: ClickHouse native port (default: 9000)
//   - CLICKHOUSE_USER: ClickHouse user (default: default)
//   - CLICKHOUSE_PASSWORD: ClickHouse password (default: empty)
//   - CLICKHOUSE_DATABASE: ClickHouse database (default: trpc_agent_go)
func newClickHouseSessionService(
	cfg SessionServiceConfig,
) (session.Service, error) {
	host := GetEnvOrDefault("CLICKHOUSE_HOST", "localhost")
	port := GetEnvOrDefault("CLICKHOUSE_PORT", "9000")
	user := GetEnvOrDefault("CLICKHOUSE_USER", "default")
	password := GetEnvOrDefault("CLICKHOUSE_PASSWORD", "")
	database := GetEnvOrDefault("CLICKHOUSE_DATABASE", "trpc_agent_go")

	dsn := fmt.Sprintf("clickhouse://%s:%s@%s:%s/%s",
		user, password, host, port, database)

	return clickhouse.NewService(
		clickhouse.WithClickHouseDSN(dsn),
		clickhouse.WithTablePrefix("trpc_"),
		clickhouse.WithSessionEventLimit(cfg.EventLimit),
		clickhouse.WithSessionTTL(cfg.TTL),
		clickhouse.WithAppendEventHook(cfg.AppendEventHooks...),
		clickhouse.WithGetSessionHook(cfg.GetSessionHooks...),
	)
}

// RunnerConfig holds configuration for creating a runner.
type RunnerConfig struct {
	AppName     string
	AgentName   string
	ModelName   string
	Instruction string
	Tools       []tool.Tool
	MaxTokens   int
	Temperature *float64
	Streaming   bool
}

// DefaultRunnerConfig returns a default runner configuration.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		AppName:     "session-demo",
		AgentName:   "demo-assistant",
		ModelName:   GetEnvOrDefault("MODEL_NAME", "deepseek-chat"),
		Instruction: "You are a helpful assistant.",
		MaxTokens:   0,
		Temperature: nil,
		Streaming:   false,
	}
}

// NewRunner creates a runner with the given session service and
// configuration.
func NewRunner(
	sessionService session.Service,
	cfg RunnerConfig,
) runner.Runner {
	modelInstance := openai.New(
		cfg.ModelName,
		openai.WithVariant(openai.VariantOpenAI),
	)

	genConfig := model.GenerationConfig{
		Temperature: cfg.Temperature,
		Stream:      cfg.Streaming,
	}
	if cfg.MaxTokens > 0 {
		genConfig.MaxTokens = IntPtr(cfg.MaxTokens)
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(cfg.Instruction),
		llmagent.WithGenerationConfig(genConfig),
	}
	if len(cfg.Tools) > 0 {
		agentOpts = append(agentOpts, llmagent.WithTools(cfg.Tools))
	}

	llmAgent := llmagent.New(cfg.AgentName, agentOpts...)

	return runner.NewRunner(
		cfg.AppName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)
}

// RunAgent runs the agent with the given message and optionally prints
// the conversation.
func RunAgent(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	message string,
	printConversation bool,
) (string, error) {
	msg := model.NewUserMessage(message)
	requestID := uuid.New().String()

	eventChan, err := r.Run(
		ctx,
		userID,
		sessionID,
		msg,
		agent.WithRequestID(requestID),
	)
	if err != nil {
		return "", fmt.Errorf("run agent failed: %w", err)
	}

	if printConversation {
		fmt.Printf("│  User: %s\n", Truncate(message, 55))
	}

	var (
		response string
		runErr   error
	)
	for evt := range eventChan {
		if evt.Error != nil && runErr == nil {
			runErr = fmt.Errorf("event error: %s", evt.Error.Message)
			continue
		}

		content := ExtractResponse(evt)
		if content != "" {
			response = content
		}
	}

	if runErr != nil {
		return "", runErr
	}

	if printConversation {
		fmt.Printf("│  Assistant: %s\n", Truncate(response, 50))
	}

	return response, nil
}

// ExtractResponse extracts the response content from an event.
func ExtractResponse(evt *event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}
	return evt.Response.Choices[0].Message.Content
}

// GetEnvOrDefault retrieves the value of an environment variable or returns a
// default value if not set.
func GetEnvOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

// Truncate truncates a string to maxLen characters.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// IntPtr returns a pointer to an int.
func IntPtr(v int) *int {
	return &v
}

// FloatPtr returns a pointer to a float64.
func FloatPtr(v float64) *float64 {
	return &v
}

// PrintSessionEvents prints all events for a session in debug mode.
// It retrieves the session from the service and prints each event's
// role and content.
func PrintSessionEvents(
	ctx context.Context,
	svc session.Service,
	appName string,
	userID string,
	sessionID string,
) error {
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session failed: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found")
	}

	events := sess.GetEvents()
	fmt.Printf("│\n")
	fmt.Printf("│  [DEBUG] Session Events: %d\n", len(events))

	for i, evt := range events {
		role := ""
		content := ""
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			role = string(evt.Response.Choices[0].Message.Role)
			content = evt.Response.Choices[0].Message.Content
		}
		content = strings.ReplaceAll(content, "\n", " ")
		fmt.Printf("│    %d. %-9s: %s\n", i+1, role, Truncate(content, 45))
	}

	return nil
}
