//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides utility functions for memory examples.
package util

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
	memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// MemoryType defines the type of memory service.
type MemoryType string

// Memory type constants.
const (
	MemoryInMemory MemoryType = "inmemory"
	MemoryRedis    MemoryType = "redis"
	MemoryPostgres MemoryType = "postgres"
	MemoryPGVector MemoryType = "pgvector"
	MemoryMySQL    MemoryType = "mysql"
)

// MemoryServiceConfig holds configuration for creating a memory service.
type MemoryServiceConfig struct {
	// Soft delete configuration.
	SoftDelete bool
	// Extractor configuration for auto memory mode.
	Extractor extractor.MemoryExtractor
	// Async memory worker configuration.
	AsyncMemoryNum   int
	MemoryQueueSize  int
	MemoryJobTimeout time.Duration
}

// RunnerConfig holds configuration for creating a runner.
type RunnerConfig struct {
	AppName     string
	AgentName   string
	ModelName   string
	Instruction string
	MaxTokens   int
	Temperature float64
	Streaming   bool
}

// DefaultRunnerConfig returns a default runner configuration.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		AppName:     "memory-chat",
		AgentName:   "memory-assistant",
		ModelName:   GetEnvOrDefault("MODEL_NAME", "deepseek-chat"),
		Instruction: "You are a helpful AI assistant with memory capabilities.",
		MaxTokens:   2000,
		Temperature: 0.7,
		Streaming:   true,
	}
}

// NewMemoryServiceByType creates a memory service based on the specified type.
//
// This function supports both manual memory mode and auto memory mode:
// - Manual mode: cfg.Extractor == nil, uses explicit memory tool calls
// - Auto mode: cfg.Extractor != nil, automatically extracts memories from conversations
//
// Parameters:
//   - memoryType: one of inmemory, redis, postgres, pgvector, mysql
//   - cfg: memory service configuration
//   - SoftDelete: enable soft delete for SQL backends
//   - Extractor: memory extractor for auto mode (nil = manual mode)
//   - AsyncMemoryNum: number of async workers for auto mode (default 1)
//   - MemoryQueueSize: queue size for memory jobs in auto mode (default 10)
//   - MemoryJobTimeout: timeout for each memory job in auto mode (default 30s)
//
// Environment variables by memory type:
//
//	redis:      REDIS_ADDR (default: localhost:6379)
//	postgres:   PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, PG_DATABASE
//	pgvector:   PGVECTOR_HOST, PGVECTOR_PORT, PGVECTOR_USER, PGVECTOR_PASSWORD, PGVECTOR_DATABASE, PGVECTOR_EMBEDDER_MODEL
//	mysql:      MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD, MYSQL_DATABASE
func NewMemoryServiceByType(memoryType MemoryType, cfg MemoryServiceConfig) (memory.Service, error) {
	switch memoryType {
	case MemoryRedis:
		return newRedisMemoryService(cfg)
	case MemoryPostgres:
		return newPostgresMemoryService(cfg)
	case MemoryPGVector:
		return newPGVectorMemoryService(cfg)
	case MemoryMySQL:
		return newMySQLMemoryService(cfg)
	case MemoryInMemory:
		fallthrough
	default:
		return newInMemoryMemoryService(cfg), nil
	}
}

// newInMemoryMemoryService creates an in-memory memory service.
// Supports both manual mode (cfg.Extractor == nil) and auto mode (cfg.Extractor != nil).
func newInMemoryMemoryService(cfg MemoryServiceConfig) memory.Service {
	opts := []memoryinmemory.ServiceOpt{}

	// Configure extractor for auto memory mode if provided.
	if cfg.Extractor != nil {
		opts = append(opts, memoryinmemory.WithExtractor(cfg.Extractor))
		if cfg.AsyncMemoryNum > 0 {
			opts = append(opts, memoryinmemory.WithAsyncMemoryNum(cfg.AsyncMemoryNum))
		}
		if cfg.MemoryQueueSize > 0 {
			opts = append(opts, memoryinmemory.WithMemoryQueueSize(cfg.MemoryQueueSize))
		}
		if cfg.MemoryJobTimeout > 0 {
			opts = append(opts, memoryinmemory.WithMemoryJobTimeout(cfg.MemoryJobTimeout))
		}
	}

	return memoryinmemory.NewMemoryService(opts...)
}

// newRedisMemoryService creates a Redis memory service.
// Supports both manual mode (cfg.Extractor == nil) and auto mode (cfg.Extractor != nil).
// Environment variables:
//   - REDIS_ADDR: Redis server address (default: localhost:6379)
func newRedisMemoryService(cfg MemoryServiceConfig) (memory.Service, error) {
	addr := GetEnvOrDefault("REDIS_ADDR", "localhost:6379")
	redisURL := fmt.Sprintf("redis://%s", addr)

	opts := []memoryredis.ServiceOpt{
		memoryredis.WithRedisClientURL(redisURL),
	}

	// Configure extractor for auto memory mode if provided.
	if cfg.Extractor != nil {
		opts = append(opts, memoryredis.WithExtractor(cfg.Extractor))
		if cfg.AsyncMemoryNum > 0 {
			opts = append(opts, memoryredis.WithAsyncMemoryNum(cfg.AsyncMemoryNum))
		}
		if cfg.MemoryQueueSize > 0 {
			opts = append(opts, memoryredis.WithMemoryQueueSize(cfg.MemoryQueueSize))
		}
		if cfg.MemoryJobTimeout > 0 {
			opts = append(opts, memoryredis.WithMemoryJobTimeout(cfg.MemoryJobTimeout))
		}
	}

	return memoryredis.NewService(opts...)
}

// newPostgresMemoryService creates a PostgreSQL memory service.
// Supports both manual mode (cfg.Extractor == nil) and auto mode (cfg.Extractor != nil).
// Environment variables:
//   - PG_HOST: PostgreSQL host (default: localhost)
//   - PG_PORT: PostgreSQL port (default: 5432)
//   - PG_USER: PostgreSQL user (default: postgres)
//   - PG_PASSWORD: PostgreSQL password (default: empty)
//   - PG_DATABASE: PostgreSQL database (default: trpc-agent-go-pgmemory)
func newPostgresMemoryService(cfg MemoryServiceConfig) (memory.Service, error) {
	host := GetEnvOrDefault("PG_HOST", "localhost")
	portStr := GetEnvOrDefault("PG_PORT", "5432")
	port := 5432
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}
	user := GetEnvOrDefault("PG_USER", "postgres")
	password := GetEnvOrDefault("PG_PASSWORD", "")
	database := GetEnvOrDefault("PG_DATABASE", "trpc-agent-go-pgmemory")

	opts := []memorypostgres.ServiceOpt{
		memorypostgres.WithHost(host),
		memorypostgres.WithPort(port),
		memorypostgres.WithUser(user),
		memorypostgres.WithPassword(password),
		memorypostgres.WithDatabase(database),
		memorypostgres.WithSoftDelete(cfg.SoftDelete),
	}

	// Configure extractor for auto memory mode if provided.
	if cfg.Extractor != nil {
		opts = append(opts, memorypostgres.WithExtractor(cfg.Extractor))
		if cfg.AsyncMemoryNum > 0 {
			opts = append(opts, memorypostgres.WithAsyncMemoryNum(cfg.AsyncMemoryNum))
		}
		if cfg.MemoryQueueSize > 0 {
			opts = append(opts, memorypostgres.WithMemoryQueueSize(cfg.MemoryQueueSize))
		}
		if cfg.MemoryJobTimeout > 0 {
			opts = append(opts, memorypostgres.WithMemoryJobTimeout(cfg.MemoryJobTimeout))
		}
	}

	return memorypostgres.NewService(opts...)
}

// newPGVectorMemoryService creates a pgvector memory service.
// Supports both manual mode (cfg.Extractor == nil) and auto mode (cfg.Extractor != nil).
// Environment variables:
//   - PGVECTOR_HOST: PostgreSQL host (default: localhost)
//   - PGVECTOR_PORT: PostgreSQL port (default: 5432)
//   - PGVECTOR_USER: PostgreSQL user (default: postgres)
//   - PGVECTOR_PASSWORD: PostgreSQL password (default: empty)
//   - PGVECTOR_DATABASE: PostgreSQL database (default: trpc-agent-go-pgmemory)
//   - PGVECTOR_EMBEDDER_MODEL: Embedder model name (default: text-embedding-3-small)
func newPGVectorMemoryService(cfg MemoryServiceConfig) (memory.Service, error) {
	host := GetEnvOrDefault("PGVECTOR_HOST", "localhost")
	portStr := GetEnvOrDefault("PGVECTOR_PORT", "5432")
	port := 5432
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}
	user := GetEnvOrDefault("PGVECTOR_USER", "postgres")
	password := GetEnvOrDefault("PGVECTOR_PASSWORD", "")
	database := GetEnvOrDefault("PGVECTOR_DATABASE", "trpc-agent-go-pgmemory")
	embedderModel := GetEnvOrDefault("PGVECTOR_EMBEDDER_MODEL", "text-embedding-3-small")

	// Create embedder - for simplicity, we'll use OpenAI embedder
	embedder := openaiembedder.New(openaiembedder.WithModel(embedderModel))

	opts := []memorypgvector.ServiceOpt{
		memorypgvector.WithHost(host),
		memorypgvector.WithPort(port),
		memorypgvector.WithUser(user),
		memorypgvector.WithPassword(password),
		memorypgvector.WithDatabase(database),
		memorypgvector.WithEmbedder(embedder),
		memorypgvector.WithSoftDelete(cfg.SoftDelete),
	}

	// Configure extractor for auto memory mode if provided.
	if cfg.Extractor != nil {
		opts = append(opts, memorypgvector.WithExtractor(cfg.Extractor))
		if cfg.AsyncMemoryNum > 0 {
			opts = append(opts, memorypgvector.WithAsyncMemoryNum(cfg.AsyncMemoryNum))
		}
		if cfg.MemoryQueueSize > 0 {
			opts = append(opts, memorypgvector.WithMemoryQueueSize(cfg.MemoryQueueSize))
		}
		if cfg.MemoryJobTimeout > 0 {
			opts = append(opts, memorypgvector.WithMemoryJobTimeout(cfg.MemoryJobTimeout))
		}
	}

	return memorypgvector.NewService(opts...)
}

// newMySQLMemoryService creates a MySQL memory service.
// Supports both manual mode (cfg.Extractor == nil) and auto mode (cfg.Extractor != nil).
// Environment variables:
//   - MYSQL_HOST: MySQL host (default: localhost)
//   - MYSQL_PORT: MySQL port (default: 3306)
//   - MYSQL_USER: MySQL user (default: root)
//   - MYSQL_PASSWORD: MySQL password (default: empty)
//   - MYSQL_DATABASE: MySQL database (default: trpc_agent_go)
func newMySQLMemoryService(cfg MemoryServiceConfig) (memory.Service, error) {
	host := GetEnvOrDefault("MYSQL_HOST", "localhost")
	port := GetEnvOrDefault("MYSQL_PORT", "3306")
	user := GetEnvOrDefault("MYSQL_USER", "root")
	password := GetEnvOrDefault("MYSQL_PASSWORD", "")
	database := GetEnvOrDefault("MYSQL_DATABASE", "trpc_agent_go")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		user, password, host, port, database)

	opts := []memorymysql.ServiceOpt{
		memorymysql.WithMySQLClientDSN(dsn),
		memorymysql.WithSoftDelete(cfg.SoftDelete),
	}

	// Configure extractor for auto memory mode if provided.
	if cfg.Extractor != nil {
		opts = append(opts, memorymysql.WithExtractor(cfg.Extractor))
		if cfg.AsyncMemoryNum > 0 {
			opts = append(opts, memorymysql.WithAsyncMemoryNum(cfg.AsyncMemoryNum))
		}
		if cfg.MemoryQueueSize > 0 {
			opts = append(opts, memorymysql.WithMemoryQueueSize(cfg.MemoryQueueSize))
		}
		if cfg.MemoryJobTimeout > 0 {
			opts = append(opts, memorymysql.WithMemoryJobTimeout(cfg.MemoryJobTimeout))
		}
	}

	return memorymysql.NewService(opts...)
}

// NewRunner creates a runner with the given memory service and configuration.
func NewRunner(memoryService memory.Service, cfg RunnerConfig) runner.Runner {
	modelInstance := openai.New(cfg.ModelName)

	genConfig := model.GenerationConfig{
		MaxTokens:   IntPtr(cfg.MaxTokens),
		Temperature: FloatPtr(cfg.Temperature),
		Stream:      cfg.Streaming,
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(cfg.Instruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(memoryService.Tools()),
	}

	llmAgent := llmagent.New(cfg.AgentName, agentOpts...)

	return runner.NewRunner(
		cfg.AppName,
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithMemoryService(memoryService),
	)
}

// GetEnvOrDefault retrieves the value of an environment variable or returns a default value if not set.
func GetEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// IntPtr returns a pointer to an int.
func IntPtr(v int) *int {
	return &v
}

// FloatPtr returns a pointer to a float64.
func FloatPtr(v float64) *float64 {
	return &v
}

// PrintMemoryInfo prints memory service information based on type.
func PrintMemoryInfo(memoryType MemoryType, softDelete bool) {
	switch memoryType {
	case MemoryRedis:
		addr := GetEnvOrDefault("REDIS_ADDR", "localhost:6379")
		fmt.Printf("Redis: %s\n", addr)
	case MemoryPostgres:
		host := GetEnvOrDefault("PG_HOST", "localhost")
		port := GetEnvOrDefault("PG_PORT", "5432")
		database := GetEnvOrDefault("PG_DATABASE", "trpc-agent-go-pgmemory")
		fmt.Printf("PostgreSQL: %s:%s/%s\n", host, port, database)
		fmt.Printf("Soft delete: %t\n", softDelete)
	case MemoryPGVector:
		host := GetEnvOrDefault("PGVECTOR_HOST", "localhost")
		port := GetEnvOrDefault("PGVECTOR_PORT", "5432")
		database := GetEnvOrDefault("PGVECTOR_DATABASE", "trpc-agent-go-pgmemory")
		embedderModel := GetEnvOrDefault("PGVECTOR_EMBEDDER_MODEL", "text-embedding-3-small")
		fmt.Printf("pgvector: %s:%s/%s\n", host, port, database)
		fmt.Printf("Embedder model: %s\n", embedderModel)
		fmt.Printf("Soft delete: %t\n", softDelete)
	case MemoryMySQL:
		host := GetEnvOrDefault("MYSQL_HOST", "localhost")
		port := GetEnvOrDefault("MYSQL_PORT", "3306")
		database := GetEnvOrDefault("MYSQL_DATABASE", "trpc_agent_go")
		fmt.Printf("MySQL: %s:%s/%s\n", host, port, database)
		fmt.Printf("Soft delete: %t\n", softDelete)
	default:
		fmt.Printf("In-memory\n")
	}
}

// GetAvailableToolsString returns a string describing available memory tools.
func GetAvailableToolsString() string {
	return "memory_add, memory_update, memory_search, memory_load\n" +
		"(memory_delete, memory_clear disabled by default, can be enabled or customized)"
}

// FormatToolCalls formats tool calls for display.
func FormatToolCalls(toolCalls []model.ToolCall) string {
	var builder strings.Builder
	for _, toolCall := range toolCalls {
		fmt.Fprintf(&builder, "   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Fprintf(&builder, "     Args: %s\n", string(toolCall.Function.Arguments))
		}
	}
	return builder.String()
}

// FormatToolResponses formats tool responses for display.
func FormatToolResponses(choices []model.Choice) string {
	var builder strings.Builder
	for _, choice := range choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Fprintf(&builder, "✅ Memory tool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
		}
	}
	return builder.String()
}
