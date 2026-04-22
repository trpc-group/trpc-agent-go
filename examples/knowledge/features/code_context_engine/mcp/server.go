//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main exposes the local trpc-agent-go code_search capability as an
// MCP server, so external MCP clients (e.g. Augment, Cursor, or another
// trpc-agent-go runner) can consume exactly the same AST-backed code search
// pipeline that comparison/local_agent.go uses.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	serverName                = "trpc-agent-code-search-mcp"
	serverVersion             = "0.1.0"
	defaultServerAddr         = ":3001"
	defaultServerPath         = "/mcp"
	repoURL                   = "https://github.com/trpc-group/trpc-agent-go"
	repoName                  = "trpc-agent-go"
	repoBranch                = "main"
	maxResults                = 5
	embeddingModelNameEnvKey  = "EMBEDDING_MODEL_NAME"
	defaultEmbeddingModelName = "server:277357"
)

// queryParamDescription tells the LLM what goes into the "query" field.
// It mirrors the QUERY GUIDELINES section of the native code_search tool
// description so that an MCP client sees the same intent-level phrasing
// rules as a direct caller of knowledgetool.NewCodeSearchTool.
const queryParamDescription = `Natural-language, intent-level question describing the code you want to find.
Phrase it as a full question you would ask a human expert, not a bag of keywords.

Good:
- "Where is interface Knowledge implemented in the default package?"
- "How does the runner propagate session state across sub-agents?"
- "Which function registers MCP tools on the streamable HTTP transport?"

Bad (too short, keyword-only, or ambiguous):
- "Knowledge"            // single symbol: use filter on metadata.trpc_ast_full_name instead
- "session isolation"    // two keywords: turn into a full question
- "error handling"       // generic: add the component, e.g. "how are tool-call errors surfaced back to the LLM?"

Leave empty only when you are doing a pure metadata/literal lookup via "filter".`

// filterParamDescription spells out the JSON shape of the structured filter
// that the native code_search tool accepts. The MCP input schema only lets
// us expose this field as a generic "object", so we push the full
// UniversalFilterCondition contract (fields, operators, enums, worked
// examples) into the description. Keep this string in sync with
// codeSearchToolDescription in knowledge/tool/codesearchtool.go.
const filterParamDescription = `Optional structured filter applied on top of the semantic query.

Shape: a UniversalFilterCondition JSON object.
- Leaf condition:    {"field": "<name>", "operator": "<op>", "value": <value>}
- Logical condition: {"operator": "and"|"or", "value": [<sub-condition>, ...]}

Operators (lowercase): eq, ne, gt, gte, lt, lte, in, not in, like, not like, between, and, or.

Available fields (use the EXACT name, including the "metadata." prefix where shown):
- metadata.trpc_ast_repo_name   enum: ["trpc-agent-go"]
- metadata.trpc_ast_scope       enum: ["code", "example"]
- metadata.trpc_ast_type        enum: ["Function","Method","Struct","Interface","Variable","Alias","Package","Class","Module","Namespace","Template","Enum","Service","RPC","Message"]
- metadata.trpc_ast_full_name   exact fully-qualified symbol name (use with eq)
- metadata.trpc_ast_package     package or module path
- metadata.trpc_ast_file_path   source file path
- metadata.trpc_ast_signature   function or method signature
- content                       raw code body; use with "like" and %...% to match literal snippets, error strings, log lines, or concrete API calls

When to use what:
- For exact symbol lookup, prefer metadata.trpc_ast_full_name + eq.
- For literal error messages, log text, SQL/HTTP fragments, or exact API calls, prefer content + like (embeddings do NOT index the full raw body).
- Combine with query (semantic) for the best precision+recall.

Examples:
1) Find a concrete API call in a specific repo:
{"operator":"and","value":[
  {"field":"metadata.trpc_ast_repo_name","operator":"eq","value":"trpc-agent-go"},
  {"field":"content","operator":"like","value":"%context.WithTimeout%"}
]}

2) Restrict to AST-labelled implementation code only:
{"operator":"and","value":[
  {"field":"metadata.trpc_ast_repo_name","operator":"eq","value":"trpc-agent-go"},
  {"field":"metadata.trpc_ast_scope","operator":"eq","value":"code"}
]}

3) Jump directly to a known symbol:
{"field":"metadata.trpc_ast_full_name","operator":"eq","value":"trpc.group/trpc-go/trpc-agent-go/agent/llmagent.LLMAgent"}`

var (
	flagAddr        = flag.String("addr", defaultServerAddr, "HTTP listen address for the MCP server")
	flagPath        = flag.String("path", defaultServerPath, "HTTP path prefix for the MCP endpoint")
	flagSkipLoad    = flag.Bool("skip-load", false, "Skip repository ingestion and reuse the existing vector-store data as-is")
	flagTruncateOld = flag.Bool("truncate-old", true, "Recreate the vector store before ingestion (deletes all existing documents); implies -skip-load=false")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		log.Fatalf("code-search MCP server exited with error: %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storeType := util.VectorStoreType(util.VectorStorePGVector)
	embeddingModelName := util.GetEnvOrDefault(
		embeddingModelNameEnvKey,
		defaultEmbeddingModelName,
	)

	skipLoad := *flagSkipLoad
	truncateOld := *flagTruncateOld
	if truncateOld && skipLoad {
		log.Printf("-truncate-old=true overrides -skip-load=true; forcing a fresh ingestion")
		skipLoad = false
	}

	log.Printf("%s v%s starting", serverName, serverVersion)
	log.Printf("repository: %s (%s)", repoURL, repoBranch)
	log.Printf("vector store: %s, embedding model: %s", storeType, embeddingModelName)
	log.Printf("skip-load: %t, truncate-old: %t", skipLoad, truncateOld)

	kb, err := buildKnowledge(storeType, embeddingModelName)
	if err != nil {
		return fmt.Errorf("build knowledge: %w", err)
	}

	if !skipLoad {
		loadCtx, loadCancel := context.WithTimeout(ctx, 30*time.Minute)
		defer loadCancel()
		loadOpts := []knowledge.LoadOption{
			knowledge.WithShowProgress(true),
			knowledge.WithDocConcurrency(15),
			knowledge.WithSourceConcurrency(10),
		}
		if truncateOld {
			log.Printf("truncate-old enabled: recreating vector store (all existing documents will be deleted)")
			loadOpts = append(loadOpts, knowledge.WithRecreate(true))
		}
		log.Printf("loading repository code into knowledge base ...")
		loadStart := time.Now()
		if err := kb.Load(loadCtx, loadOpts...); err != nil {
			return fmt.Errorf("knowledge.Load failed: %w", err)
		}
		util.WaitForIndexRefresh(storeType)
		log.Printf("knowledge.Load completed in %s", time.Since(loadStart).Round(time.Millisecond))
	} else {
		log.Printf("reuse existing vector-store data, skip knowledge.Load")
	}

	searchTool := knowledgetool.NewCodeSearchTool(
		kb,
		knowledgetool.WithCodeSearchMaxResults(maxResults),
	)
	callable, ok := searchTool.(agenttool.CallableTool)
	if !ok {
		return fmt.Errorf("code_search tool does not implement CallableTool")
	}
	decl := searchTool.Declaration()
	if decl == nil {
		return fmt.Errorf("code_search tool has nil declaration")
	}

	server := mcp.NewServer(
		serverName,
		serverVersion,
		mcp.WithServerAddress(*flagAddr),
		mcp.WithServerPath(*flagPath),
		mcp.WithServerLogger(mcp.GetDefaultLogger()),
	)

	mcpTool := mcp.NewTool(
		decl.Name,
		mcp.WithDescription(decl.Description),
		mcp.WithString("query",
			mcp.Description(queryParamDescription),
		),
		mcp.WithObject("filter",
			mcp.Description(filterParamDescription),
		),
	)

	handler := newCodeSearchHandler(callable)
	server.RegisterTool(mcpTool, handler)
	log.Printf("registered MCP tool: %s", decl.Name)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("MCP server listening on %s%s", *flagAddr, *flagPath)
		serverErr <- server.Start()
	}()

	select {
	case sig := <-stop:
		log.Printf("received signal %s, shutting down", sig)
		return nil
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("MCP server exited: %w", err)
		}
		return nil
	}
}

func buildKnowledge(
	storeType util.VectorStoreType,
	embeddingModelName string,
) (*knowledge.BuiltinKnowledge, error) {
	emb := openaiembedder.New(openaiembedder.WithModel(embeddingModelName))

	vs, err := util.NewVectorStoreByTypeWithDimension(storeType, emb.GetDimensions())
	if err != nil {
		return nil, fmt.Errorf("create vector store: %w", err)
	}

	repoSource := repo.New(
		repo.WithRepository(repo.Repository{
			URL:         repoURL,
			Branch:      repoBranch,
			RepoName:    repoName,
			Description: "tRPC agent framework for Go (this repository).",
			RepoURL:     repoURL,
		}),
		repo.WithFileExtensions([]string{".go", ".md"}),
		repo.WithSkipSuffixes([]string{".pb.go", ".pb.grpc.go", ".trpc.go", "_mock.go", "_test.go"}),
	)

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources([]source.Source{repoSource}),
	)
	return kb, nil
}

// newCodeSearchHandler bridges an MCP CallToolRequest to the underlying
// trpc-agent-go CallableTool. The MCP arguments are re-encoded to JSON and
// forwarded as-is, so the behavior matches what comparison/local_agent.go
// sees when the local LLMAgent calls code_search directly.
func newCodeSearchHandler(callable agenttool.CallableTool) func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-ctx.Done():
			return mcp.NewErrorResult("request cancelled"), ctx.Err()
		default:
		}

		args := req.Params.Arguments
		if args == nil {
			args = map[string]any{}
		}
		if q, ok := args["query"].(string); !ok || strings.TrimSpace(q) == "" {
			return mcp.NewErrorResult("argument 'query' is required and must be a non-empty string"), nil
		}

		jsonArgs, err := json.Marshal(args)
		if err != nil {
			return mcp.NewErrorResult(fmt.Sprintf("marshal arguments: %v", err)), nil
		}

		result, err := callable.Call(ctx, jsonArgs)
		if err != nil {
			return mcp.NewErrorResult(fmt.Sprintf("code_search call failed: %v", err)), nil
		}

		text, err := renderToolResult(result)
		if err != nil {
			return mcp.NewErrorResult(fmt.Sprintf("render tool result: %v", err)), nil
		}
		return mcp.NewTextResult(text), nil
	}
}

func renderToolResult(result any) (string, error) {
	if result == nil {
		return "", nil
	}
	switch v := result.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal result: %w", err)
		}
		return string(data), nil
	}
}
