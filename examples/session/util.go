//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/mysql"
	"trpc.group/trpc-go/trpc-agent-go/session/postgres"
	"trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SessionType defines the type of session service.
type SessionType string

// Session type constants.
const (
	SessionInMemory   SessionType = "inmemory"
	SessionRedis      SessionType = "redis"
	SessionPostgres   SessionType = "postgres"
	SessionMySQL      SessionType = "mysql"
	SessionClickHouse SessionType = "clickhouse"
)

// SessionServiceConfig holds configuration for creating a session service.
type SessionServiceConfig struct {
	EventLimit       int
	TTL              time.Duration
	AppendEventHooks []session.AppendEventHook
	GetSessionHooks  []session.GetSessionHook
}

// NewSessionServiceByType creates a session service based on the specified type.
//
// Parameters:
//   - sessionType: one of inmemory, redis, postgres, mysql, clickhouse
//   - cfg: session service configuration (eventLimit, ttl, hooks)
//
// Environment variables by session type:
//
//	redis:      REDIS_ADDR (default: localhost:6379)
//	postgres:   PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, PG_DATABASE
//	mysql:      MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD, MYSQL_DATABASE
//	clickhouse: CLICKHOUSE_HOST, CLICKHOUSE_PORT, CLICKHOUSE_USER, CLICKHOUSE_PASSWORD, CLICKHOUSE_DATABASE
func NewSessionServiceByType(sessionType SessionType, cfg SessionServiceConfig) (session.Service, error) {
	switch sessionType {
	case SessionRedis:
		return newRedisSessionService(cfg)
	case SessionPostgres:
		return newPostgresSessionService(cfg)
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
func newRedisSessionService(cfg SessionServiceConfig) (session.Service, error) {
	addr := GetEnvOrDefault("REDIS_ADDR", "localhost:6379")
	redisURL := fmt.Sprintf("redis://%s", addr)

	return redis.NewService(
		redis.WithRedisClientURL(redisURL),
		redis.WithSessionEventLimit(cfg.EventLimit),
		redis.WithSessionTTL(cfg.TTL),
		redis.WithAppendEventHook(cfg.AppendEventHooks...),
		redis.WithGetSessionHook(cfg.GetSessionHooks...),
	)
}

// newPostgresSessionService creates a PostgreSQL session service.
// Environment variables:
//   - PG_HOST: PostgreSQL host (default: localhost)
//   - PG_PORT: PostgreSQL port (default: 5432)
//   - PG_USER: PostgreSQL user (default: root)
//   - PG_PASSWORD: PostgreSQL password (default: empty)
//   - PG_DATABASE: PostgreSQL database (default: trpc_agent_go)
func newPostgresSessionService(cfg SessionServiceConfig) (session.Service, error) {
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

// newMySQLSessionService creates a MySQL session service.
// Environment variables:
//   - MYSQL_HOST: MySQL host (default: localhost)
//   - MYSQL_PORT: MySQL port (default: 3306)
//   - MYSQL_USER: MySQL user (default: root)
//   - MYSQL_PASSWORD: MySQL password (default: empty)
//   - MYSQL_DATABASE: MySQL database (default: trpc_agent_go)
func newMySQLSessionService(cfg SessionServiceConfig) (session.Service, error) {
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
func newClickHouseSessionService(cfg SessionServiceConfig) (session.Service, error) {
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
	Temperature float64
	Streaming   bool
}

// DefaultRunnerConfig returns a default runner configuration.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		AppName:     "session-demo",
		AgentName:   "demo-assistant",
		ModelName:   GetEnvOrDefault("MODEL_NAME", "deepseek-chat"),
		Instruction: "You are a helpful assistant.",
		MaxTokens:   100,
		Temperature: 0.1,
		Streaming:   false,
	}
}

// NewRunner creates a runner with the given session service and configuration.
func NewRunner(sessionService session.Service, cfg RunnerConfig) runner.Runner {
	modelInstance := openai.New(cfg.ModelName, openai.WithVariant(openai.VariantOpenAI))

	genConfig := model.GenerationConfig{
		MaxTokens:   IntPtr(cfg.MaxTokens),
		Temperature: FloatPtr(cfg.Temperature),
		Stream:      cfg.Streaming,
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

// RunAgent runs the agent with the given message and optionally prints the conversation.
func RunAgent(ctx context.Context, r runner.Runner, userID, sessionID, message string, printConversation bool) (string, error) {
	msg := model.NewUserMessage(message)
	requestID := uuid.New().String()

	eventChan, err := r.Run(ctx, userID, sessionID, msg, agent.WithRequestID(requestID))
	if err != nil {
		return "", fmt.Errorf("run agent failed: %w", err)
	}

	if printConversation {
		fmt.Printf("│  User: %s\n", Truncate(message, 55))
	}

	var response string
	for evt := range eventChan {
		if evt.Error != nil {
			return "", fmt.Errorf("event error: %s", evt.Error.Message)
		}
		response = ExtractResponse(evt)
		if evt.IsFinalResponse() {
			break
		}
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

// GetEnvOrDefault retrieves the value of an environment variable or returns a default value if not set.
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
// It retrieves the session from the service and prints each event's role and content.
func PrintSessionEvents(ctx context.Context, svc session.Service, appName, userID, sessionID string) error {
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session failed: %w", err)
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
