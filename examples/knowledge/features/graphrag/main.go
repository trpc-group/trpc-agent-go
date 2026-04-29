//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates GraphRAG over repository code in a multi-turn chat.
// It loads the trpc-go repository through repo source, wires graph search,
// traversal, and path tools into an LLM agent, and prints tool calls and
// tool responses during the conversation.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for the chat model and embeddings.
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint.
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-chat.
//   - EMBEDDING_MODEL: (Optional) Embedding model, defaults to server:277357.
//   - EMBEDDING_DIMENSION: (Optional) Embedding dimension. If unset, the example probes once.
//   - AGE_DSN: (Optional) PostgreSQL DSN for Apache AGE, highest priority.
//   - AGE_HOST: (Optional) PostgreSQL host for Apache AGE, defaults to 127.0.0.1.
//   - AGE_PORT: (Optional) PostgreSQL port for Apache AGE, defaults to 5432.
//   - AGE_USER: (Optional) PostgreSQL user for Apache AGE, defaults to root.
//   - AGE_PASSWORD: (Optional) PostgreSQL password for Apache AGE, defaults to 123.
//   - AGE_DATABASE: (Optional) PostgreSQL database for Apache AGE, defaults to contextengine.
//   - AGE_GRAPH_NAME: (Optional) Apache AGE graph name, defaults to knowledge_graph.
//   - PGVECTOR_DSN: (Optional) PostgreSQL DSN for pgvector, highest priority.
//   - PGVECTOR_HOST: (Optional) PostgreSQL host for pgvector, defaults to 127.0.0.1.
//   - PGVECTOR_PORT: (Optional) PostgreSQL port for pgvector, defaults to 5432.
//   - PGVECTOR_USER: (Optional) PostgreSQL user for pgvector, defaults to root.
//   - PGVECTOR_PASSWORD: (Optional) PostgreSQL password for pgvector, defaults to 123.
//   - PGVECTOR_DATABASE: (Optional) PostgreSQL database for pgvector, defaults to contextengine.
//   - PGVECTOR_TABLE: (Optional) PostgreSQL pgvector table, defaults to trpc_agent_go_graph.
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export MODEL_NAME=deepseek-chat
//	go run ./features/graphrag
package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	agegraphstore "trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore/age"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	defaultRepoURL = "https://github.com/trpc-group/trpc-go"
	defaultQuery   = "Find code related to client RPC invocation in trpc-go, traverse its callees, and explain the nearby call graph."
	query          = flag.String("query", "", "Optional initial query to ask before entering chat")
	modelName      = flag.String("model", util.GetEnvOrDefault("MODEL_NAME", "deepseek-v3,2"), "Model to use")
	streaming      = flag.Bool("streaming", true, "Enable streaming mode for responses")
	embeddingModel = flag.String("embedding-model", util.GetEnvOrDefault("EMBEDDING_MODEL", "server:277357"), "Embedding model to use")
	embeddingDim   = flag.Int("embedding-dimension", defaultEmbeddingDimension(), "Embedding dimension; <=0 probes once")
	recreate       = flag.Bool("recreate", false, "Drop pgvector table and reload graph source; false reuses existing graph/vector data")
	progressStep   = flag.Int("progress-step", 100, "Graph seed indexing progress step")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	fmt.Println("GraphRAG Chat Demo")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	ageDSN, ageGraphName := ageConfig()
	if *recreate {
		if err := dropAGEGraph(ctx, ageDSN, ageGraphName); err != nil {
			log.Fatalf("drop AGE graph: %v", err)
		}
		fmt.Printf("Dropped AGE graph: %s\n", ageGraphName)
	}

	graphStore, err := newAGEGraphStore()
	if err != nil {
		log.Fatalf("create AGE graph store: %v", err)
	}
	defer graphStore.Close()

	embedder := newOpenAIEmbedder(*embeddingDim)
	resolvedDimension, err := resolveEmbeddingDimension(ctx, embedder, *embeddingDim)
	if err != nil {
		log.Fatalf("resolve embedding dimension: %v", err)
	}
	if *embeddingDim <= 0 {
		embedder = newOpenAIEmbedder(resolvedDimension)
	}
	fmt.Printf("Embedding: %s (%d dimensions)\n", *embeddingModel, resolvedDimension)
	vectorDSN, vectorTable := pgVectorConfig()

	if *recreate {
		if err := dropPGVectorTable(ctx, vectorDSN, vectorTable); err != nil {
			log.Fatalf("drop pgvector table: %v", err)
		}
		fmt.Printf("Dropped pgvector table: %s\n", vectorTable)
	}

	vectorStore, err := newVectorStore(vectorDSN, vectorTable, resolvedDimension)
	if err != nil {
		log.Fatalf("create vector store: %v", err)
	}
	defer vectorStore.Close()

	kb := knowledge.NewGraphKnowledge(
		knowledge.WithGraphStore(graphStore),
		knowledge.WithGraphVectorStore(vectorStore),
		knowledge.WithGraphEmbedder(embedder),
	)
	if *recreate {
		repoSrc := repo.New(
			repo.WithRepository(repo.Repository{
				URL:         defaultRepoURL,
				RepoName:    "trpc-go",
				RepoURL:     defaultRepoURL,
				Description: "The tRPC-Go framework repository used to demonstrate graph RAG over repo source.",
			}),
			repo.WithName("trpc-go Repository"),
			repo.WithFileExtensions([]string{".go"}),
		)
		if err := kb.LoadGraphSource(
			ctx,
			repoSrc,
			knowledge.WithShowProgress(true),
			knowledge.WithProgressStepSize(*progressStep),
			knowledge.WithSourceConcurrency(10),
			knowledge.WithDocConcurrency(10),
		); err != nil {
			log.Fatalf("load graph from repo source: %v", err)
		}
		fmt.Printf("Loaded graph from repo source: %s\n", defaultRepoURL)
	} else {
		fmt.Println("Skipped graph source loading; using existing AGE graph and pgvector data")
	}

	graphToolSet := knowledgetool.NewCodeGraphSearchTool(
		kb,
		knowledgetool.WithCodeSearchRepoInfos([]knowledgetool.CodeRepoInfo{{
			Name:        "trpc-go",
			Description: "The tRPC-Go framework repository used to demonstrate graph RAG over repo source.",
		}}),
		knowledgetool.WithCodeSearchMaxResults(3),
	)

	llmAgent := llmagent.New(
		"graph-knowledge-assistant",
		llmagent.WithModel(openaimodel.New(*modelName)),
		llmagent.WithInstruction(graphAgentInstruction()),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming}),
		llmagent.WithToolSets([]tool.ToolSet{graphToolSet}),
	)

	r := runner.NewRunner(
		"graph-knowledge-chat",
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	chat := &graphChat{
		runner:    r,
		userID:    "graph-demo-user",
		sessionID: fmt.Sprintf("graph-demo-session-%d", time.Now().Unix()),
		streaming: *streaming,
	}
	if strings.TrimSpace(*query) != "" {
		fmt.Printf("\nInitial query: %s\n", *query)
		if err := chat.processMessage(ctx, *query); err != nil {
			log.Fatalf("run initial query: %v", err)
		}
		fmt.Println()
	}
	if err := chat.start(ctx); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

type graphChat struct {
	runner    runner.Runner
	userID    string
	sessionID string
	streaming bool
}

func (c *graphChat) start(ctx context.Context) error {
	fmt.Printf("\nChat ready. Session: %s\n", c.sessionID)
	fmt.Printf("Type '/exit' to end the conversation.\n")
	fmt.Printf("Try: %s\n", defaultQuery)
	fmt.Println(strings.Repeat("=", 50))

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
		if userInput == "/exit" || userInput == "/quit" {
			fmt.Println("Done.")
			return nil
		}
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *graphChat) processMessage(ctx context.Context, userMessage string) error {
	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
		agent.WithRequestID(uuid.New().String()),
	)
	if err != nil {
		return fmt.Errorf("run graph knowledge agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func (c *graphChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("Assistant: ")
	assistantStarted := true
	toolCallsDetected := false
	for evt := range eventChan {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("\nEvent error: %s\n", evt.Error.Message)
			continue
		}
		if c.handleToolCalls(evt, &assistantStarted, &toolCallsDetected) {
			continue
		}
		if c.handleToolResponses(evt, &assistantStarted) {
			continue
		}
		c.handleContent(evt, &assistantStarted, toolCallsDetected)
		if evt.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return nil
}

func (c *graphChat) handleToolCalls(evt *event.Event, assistantStarted *bool, toolCallsDetected *bool) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	calls := evt.Response.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		return false
	}
	*toolCallsDetected = true
	if *assistantStarted {
		fmt.Println()
	}
	fmt.Println()
	fmt.Printf("Tool calls (%d):\n", len(calls))
	for i, call := range calls {
		fmt.Printf("  [%d] %s id=%s\n", i+1, call.Function.Name, call.ID)
		if len(call.Function.Arguments) > 0 {
			fmt.Println("      args:")
			fmt.Println(indentBlock(formatToolArguments(string(call.Function.Arguments)), "        "))
		}
	}
	fmt.Println("Executing tools...")
	fmt.Println()
	*assistantStarted = false
	return true
}

func (c *graphChat) handleToolResponses(evt *event.Event, assistantStarted *bool) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	printed := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role != model.RoleTool || choice.Message.ToolID == "" {
			continue
		}
		if printed {
			fmt.Println()
		}
		fmt.Printf("Tool result: %s id=%s\n", choice.Message.ToolName, choice.Message.ToolID)
		fmt.Println(formatToolResponse(choice.Message.Content))
		printed = true
	}
	if printed {
		fmt.Println()
		*assistantStarted = false
	}
	return printed
}

func (c *graphChat) handleContent(evt *event.Event, assistantStarted *bool, toolCallsDetected bool) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	choice := evt.Response.Choices[0]
	content := choice.Delta.Content
	if !c.streaming && content == "" {
		content = choice.Message.Content
	}
	if content == "" {
		return
	}
	if !*assistantStarted {
		if toolCallsDetected {
			fmt.Print("Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
}

func defaultEmbeddingDimension() int {
	dimension := strings.TrimSpace(util.GetEnvOrDefault("EMBEDDING_DIMENSION", "0"))
	value, err := strconv.Atoi(dimension)
	if err != nil {
		return 0
	}
	return value
}

func newOpenAIEmbedder(dimension int) *openaiembedder.Embedder {
	opts := []openaiembedder.Option{openaiembedder.WithModel(*embeddingModel)}
	if dimension > 0 {
		opts = append(opts, openaiembedder.WithDimensions(dimension))
	}
	return openaiembedder.New(opts...)
}

func resolveEmbeddingDimension(ctx context.Context, embedder *openaiembedder.Embedder, configured int) (int, error) {
	if configured > 0 {
		return configured, nil
	}
	embedding, err := embedder.GetEmbedding(ctx, "dimension probe")
	if err != nil {
		return 0, err
	}
	if len(embedding) == 0 {
		return 0, fmt.Errorf("embedding probe returned empty vector")
	}
	return len(embedding), nil
}

func newAGEGraphStore() (*agegraphstore.Store, error) {
	dsn, graphName := ageConfig()
	return agegraphstore.New(
		agegraphstore.WithClientDSN(dsn),
		agegraphstore.WithGraphName(graphName),
	)
}

func ageConfig() (string, string) {
	host := util.GetEnvOrDefault("AGE_HOST", "127.0.0.1")
	port := util.GetEnvOrDefault("AGE_PORT", "5432")
	user := util.GetEnvOrDefault("AGE_USER", "root")
	password := util.GetEnvOrDefault("AGE_PASSWORD", "123")
	database := util.GetEnvOrDefault("AGE_DATABASE", "contextengine")
	graphName := util.GetEnvOrDefault("AGE_GRAPH_NAME", "knowledge_graph")

	dsn := strings.TrimSpace(util.GetEnvOrDefault("AGE_DSN", ""))
	if dsn == "" {
		dsn = (&url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(user, password),
			Host:     net.JoinHostPort(host, port),
			Path:     database,
			RawQuery: "sslmode=disable",
		}).String()
	}
	return dsn, graphName
}

func pgVectorConfig() (string, string) {
	host := util.GetEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	port := util.GetEnvOrDefault("PGVECTOR_PORT", "5432")
	user := util.GetEnvOrDefault("PGVECTOR_USER", "root")
	password := util.GetEnvOrDefault("PGVECTOR_PASSWORD", "123")
	database := util.GetEnvOrDefault("PGVECTOR_DATABASE", "contextengine")
	table := util.GetEnvOrDefault("PGVECTOR_TABLE", "trpc_agent_go_graph")

	dsn := strings.TrimSpace(util.GetEnvOrDefault("PGVECTOR_DSN", ""))
	if dsn == "" {
		dsn = (&url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(user, password),
			Host:     net.JoinHostPort(host, port),
			Path:     database,
			RawQuery: "sslmode=disable",
		}).String()
	}
	return dsn, table
}

func dropPGVectorTable(ctx context.Context, dsn, table string) error {
	client, err := postgres.GetClientBuilder()(ctx, postgres.WithClientConnString(dsn))
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteQualifiedIdentifier(table)+" CASCADE")
	return err
}

func dropAGEGraph(ctx context.Context, dsn, graphName string) error {
	client, err := postgres.GetClientBuilder()(ctx, postgres.WithClientConnString(dsn))
	if err != nil {
		return err
	}
	defer client.Close()
	if _, err := client.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS age"); err != nil {
		return err
	}
	var exists bool
	if err := client.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&exists)
		}
		return nil
	}, "SELECT EXISTS(SELECT 1 FROM ag_catalog.ag_graph WHERE name = $1)", graphName); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := client.ExecContext(ctx, "LOAD 'age'"); err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, "SET search_path = ag_catalog, public"); err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, "SELECT ag_catalog.drop_graph($1, true)", graphName)
	return err
}

func quoteQualifiedIdentifier(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

func newVectorStore(dsn, table string, indexDimension int) (*pgvector.VectorStore, error) {
	return pgvector.New(
		pgvector.WithPGVectorClientDSN(dsn),
		pgvector.WithTable(table),
		pgvector.WithIndexDimension(indexDimension),
	)
}

func graphAgentInstruction() string {
	return strings.Join([]string{
		"You are a code graph assistant.",
		"Use code_graph_search only to find seed code graph nodes.",
		"For mechanism, architecture, flow, lifecycle, or implementation questions, do not answer from search results alone: after finding a relevant seed, call code_graph_traverse at least once.",
		"Do not repeat near-identical code_graph_search calls after useful results or duplicate-result messages; traverse a known candidate or answer from gathered evidence.",
		"Use code_graph_traverse when the user asks about neighbors, callees, callers, dependencies, local graph context, or how a mechanism is wired.",
		"Use code_graph_find_paths when the user asks how two code entities are connected.",
		"When calling code_graph_traverse, prefer query plus a metadata filter instead of guessing node IDs.",
		"When calling code_graph_find_paths, prefer from_query/from_filter and to_query/to_filter instead of guessing node IDs.",
		"For function execution flow, traverse direction \"out\" with edge_types [\"CALLS\"]. For callers, traverse direction \"in\" with edge_types [\"CALLS\"]. For type structure, use METHOD, FIELD, PARAM, RETURNS, ALIAS_OF, or IMPLEMENTS as appropriate.",
		"Keep tool use concise: usually 1-2 seed searches plus 1-2 targeted traversals are enough before answering.",
		"Ground the final answer in the returned node names and edge directions.",
	}, "\n")
}
