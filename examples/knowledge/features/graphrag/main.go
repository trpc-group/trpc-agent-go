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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/golang"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	agegraphstore "trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore/age"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
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
	modelName      = flag.String("model", util.GetEnvOrDefault("MODEL_NAME", "deepseek-chat"), "Model to use")
	embeddingModel = flag.String("embedding-model", util.GetEnvOrDefault("EMBEDDING_MODEL", "server:277357"), "Embedding model to use")
	embeddingDim   = flag.Int("embedding-dimension", defaultEmbeddingDimension(), "Embedding dimension; <=0 probes once")
	recreate       = flag.Bool("recreate", false, "Drop pgvector table and reload graph source; false reuses existing graph/vector data")
	progressStep   = flag.Int("progress-step", 1000, "Graph seed indexing progress step")
	debugFile      = flag.String("debug-file", "", "Write JSONL tool trace to this file when set")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	fmt.Println("GraphRAG Chat Demo")
	fmt.Printf("Model: %s\n", *modelName)
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

	embedder, resolvedDimension, err := setupEmbedder(ctx)
	if err != nil {
		log.Fatalf("setup embedder: %v", err)
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
		if err := loadGraphSource(ctx, kb); err != nil {
			log.Fatalf("load graph from repo source: %v", err)
		}
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
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		llmagent.WithToolSets([]tool.ToolSet{graphToolSet}),
	)

	r := runner.NewRunner(
		"graph-knowledge-chat",
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	var trace *jsonlTrace
	if *debugFile != "" {
		trace, err = newJSONLTrace(*debugFile)
		if err != nil {
			log.Fatalf("open debug file: %v", err)
		}
		defer trace.Close()
		fmt.Printf("Debug trace: %s\n", *debugFile)
	}

	sessionID := fmt.Sprintf("graph-demo-session-%d", time.Now().Unix())
	fmt.Printf("\nChat ready. Session: %s\n", sessionID)
	fmt.Printf("Type '/exit' to end the conversation.\n")
	fmt.Printf("Try: %s\n", defaultQuery)
	fmt.Println(strings.Repeat("=", 50))

	if strings.TrimSpace(*query) != "" {
		fmt.Printf("\nInitial query: %s\n", *query)
		if err := runTurn(ctx, r, "graph-demo-user", sessionID, *query, trace); err != nil {
			fmt.Printf("run initial query failed: %v\n", err)
			return
		}
		fmt.Println()
	}

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
			return
		}
		if err := runTurn(ctx, r, "graph-demo-user", sessionID, userInput, trace); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("input scanner: %v", err)
	}
}

// runTurn sends one user message and prints the streamed response with tool activity.
func runTurn(ctx context.Context, r runner.Runner, userID, sessionID, userMessage string, trace *jsonlTrace) error {
	trace.write("user_message", map[string]any{"message": userMessage})

	eventChan, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(userMessage))
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}

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
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}

		for ci, choice := range evt.Response.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				toolCallsDetected = true
				if assistantStarted {
					fmt.Println()
				}
				fmt.Println()
				fmt.Printf("Tool calls (%d):\n", len(choice.Message.ToolCalls))
				for i, call := range choice.Message.ToolCalls {
					fmt.Printf("  [%d] %s id=%s\n", i+1, call.Function.Name, call.ID)
					if len(call.Function.Arguments) > 0 {
						fmt.Println("      args:")
						fmt.Println(indentBlock(formatToolArguments(string(call.Function.Arguments)), "        "))
					}
					trace.write("tool_call", map[string]any{
						"tool_name": call.Function.Name,
						"tool_id":   call.ID,
						"arguments": decodeJSONValue(string(call.Function.Arguments)),
					})
				}
				fmt.Println("Executing tools...")
				fmt.Println()
				assistantStarted = false
				continue
			}

			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				fmt.Printf("Tool result: %s id=%s\n", choice.Message.ToolName, choice.Message.ToolID)
				fmt.Println(formatToolResponse(choice.Message.Content))
				fmt.Println()
				trace.write("tool_result", map[string]any{
					"tool_name": choice.Message.ToolName,
					"tool_id":   choice.Message.ToolID,
					"result":    decodeJSONValue(choice.Message.Content),
				})
				assistantStarted = false
				continue
			}

			if ci > 0 {
				continue
			}
			content := choice.Delta.Content
			if content == "" {
				content = choice.Message.Content
			}
			if content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Print("Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(content)
			}
		}
		if evt.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return nil
}

// timedGraphSource wraps a GraphSource and logs parse timing.
type timedGraphSource struct {
	name string
	src  source.GraphSource
}

func (s timedGraphSource) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	start := time.Now()
	data, err := s.src.ReadGraph(ctx, opts...)
	elapsed := time.Since(start).Truncate(time.Millisecond)
	if err != nil {
		fmt.Printf("Graph source parse failed: %s | elapsed=%s\n", s.name, elapsed)
		return nil, err
	}
	fmt.Printf("Graph source parsed: %s | nodes=%d edges=%d elapsed=%s\n", s.name, len(data.Nodes), len(data.Edges), elapsed)
	return data, nil
}

// loadGraphSource loads the trpc-go repo into the graph knowledge base.
func loadGraphSource(ctx context.Context, kb *knowledge.BuiltinGraphKnowledge) error {
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
	err := kb.LoadGraphSource(
		ctx,
		timedGraphSource{name: "trpc-go Repository", src: repoSrc},
		knowledge.WithGraphLoadProgress(true),
		knowledge.WithGraphLoadProgressStepSize(*progressStep),
		knowledge.WithGraphLoadConcurrency(knowledge.GraphLoadConcurrency{
			AddNodeRoutines:   200,
			AddEdgeRoutines:   200,
			EmbeddingRoutines: 10,
		}),
		knowledge.WithGraphLoadReadGraphOpts(source.WithReadGraphParseConcurrency(300)),
	)
	if err != nil {
		return err
	}
	fmt.Printf("Loaded graph from repo source: %s\n", defaultRepoURL)
	return nil
}

// jsonlTrace appends JSON lines to a file for debugging tool activity.
// A nil receiver is safe to call on all methods.
type jsonlTrace struct {
	file    *os.File
	encoder *json.Encoder
}

func newJSONLTrace(path string) (*jsonlTrace, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	return &jsonlTrace{file: f, encoder: json.NewEncoder(f)}, nil
}

func (t *jsonlTrace) write(kind string, fields map[string]any) {
	if t == nil {
		return
	}
	fields["kind"] = kind
	fields["timestamp"] = time.Now().Format(time.RFC3339Nano)
	if err := t.encoder.Encode(fields); err != nil {
		log.Printf("write debug trace: %v", err)
	}
}

func decodeJSONValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return raw
	}
	return value
}

func (t *jsonlTrace) Close() error {
	if t == nil || t.file == nil {
		return nil
	}
	return t.file.Close()
}

func setupEmbedder(ctx context.Context) (*openaiembedder.Embedder, int, error) {
	opts := []openaiembedder.Option{
		openaiembedder.WithModel(*embeddingModel),
		openaiembedder.WithMaxRetries(20),
	}
	if *embeddingDim > 0 {
		opts = append(opts, openaiembedder.WithDimensions(*embeddingDim))
	}
	e := openaiembedder.New(opts...)
	if *embeddingDim > 0 {
		return e, *embeddingDim, nil
	}
	vec, err := e.GetEmbedding(ctx, "dimension probe")
	if err != nil {
		return nil, 0, err
	}
	if len(vec) == 0 {
		return nil, 0, fmt.Errorf("embedding probe returned empty vector")
	}
	dim := len(vec)
	return openaiembedder.New(append(opts, openaiembedder.WithDimensions(dim))...), dim, nil
}

func defaultEmbeddingDimension() int {
	value, err := strconv.Atoi(strings.TrimSpace(util.GetEnvOrDefault("EMBEDDING_DIMENSION", "0")))
	if err != nil {
		return 0
	}
	return value
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
		"Use code_graph_search only to find seed code graph nodes and collect their graph node IDs.",
		"For mechanism, architecture, flow, lifecycle, or implementation questions, do not answer from search results alone: after finding a relevant seed node ID, call code_graph_traverse at least once.",
		"Do not repeat near-identical code_graph_search calls after useful results or duplicate-result messages; traverse a known candidate or answer from gathered evidence.",
		"",
		"== code_graph_traverse usage ==",
		"code_graph_traverse requires start_ids — graph node IDs returned by code_graph_search. It has NO query or filter parameter. Always pass IDs, not names or package paths.",
		"Workflow: (1) call code_graph_search to find relevant symbols and collect their 'id' fields, (2) pass those IDs as start_ids to code_graph_traverse.",
		"IMPORTANT: as soon as code_graph_search returns a relevant interface or type node ID, immediately call code_graph_traverse with IMPLEMENTS/METHOD/FIELD edges. Do NOT keep calling code_graph_search to enumerate package members — traverse does that in one call.",
		"To find all implementations of an interface: search for the interface → get its ID → traverse with edge_types [\"IMPLEMENTS\"], direction \"in\", max_depth 1.",
		"To find callers of a function: traverse direction \"in\", edge_types [\"CALLS\"], max_depth 1.",
		"To find callees of a function: traverse direction \"out\", edge_types [\"CALLS\"], max_depth 1.",
		"To inspect a type's methods or fields: traverse direction \"out\", edge_types [\"METHOD\"] or [\"FIELD\"], max_depth 1.",
		"",
		"== code_graph_find_paths usage ==",
		"Use code_graph_find_paths when the user asks how two specific code entities are connected.",
		"Before calling code_graph_find_paths, resolve both endpoints with code_graph_search and pass the returned graph node IDs as from_id and to_id.",
		"",
		"Keep tool use concise: usually 1-2 seed searches plus 1-2 targeted traversals are enough before answering.",
		"Ground the final answer in the returned node names and edge directions.",
	}, "\n")
}
