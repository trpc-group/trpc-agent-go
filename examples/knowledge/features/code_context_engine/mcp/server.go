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
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	serverName                = "trpc-agent-code-search-mcp"
	serverVersion             = "0.1.0"
	defaultServerAddr         = "127.0.0.1:3001"
	defaultServerPath         = "/mcp"
	repoURL                   = "https://github.com/trpc-group/trpc-agent-go"
	repoName                  = "trpc-agent-go"
	repoBranch                = "main"
	maxResults                = 5
	embeddingModelNameEnvKey  = "EMBEDDING_MODEL_NAME"
	defaultEmbeddingModelName = "text-embedding-3-small"
	vectorStoreTypeEnvKey     = "VECTOR_STORE_TYPE"
	filterSchemaDepth         = 2
)

const (
	codeSearchQueryDescription          = "The search query to find relevant information in the knowledge base. Can be empty when using only filters."
	codeSearchFilterDescription         = "Filter conditions to apply to the search query. Use lowercase operators eq ne gt gte lt lte in not in like not like between and or."
	codeSearchFilterFieldDescription    = "The metadata field to filter on. Use it for comparison operators and ignore it for logical operators and or."
	codeSearchFilterOperatorDescription = "The operator to use. Valid values are eq ne gt gte lt lte in not in like not like between and or."
	codeSearchFilterValueDescription    = "Comparison value for eq ne gt gte lt lte. Use an array for in not in between. Use an array of nested filter conditions for and or."
)

var (
	flagAddr        = flag.String("addr", defaultServerAddr, "HTTP listen address for the MCP server")
	flagPath        = flag.String("path", defaultServerPath, "HTTP path prefix for the MCP endpoint")
	flagSkipLoad    = flag.Bool("skip-load", false, "Skip repository ingestion and reuse the existing vector-store data as-is")
	flagTruncateOld = flag.Bool("truncate-old", false, "Recreate the vector store before ingestion (deletes all existing documents); implies -skip-load=false")
	flagStoreType   = flag.String("store", util.GetEnvOrDefault(vectorStoreTypeEnvKey, string(util.VectorStoreInMemory)), "Vector store type (inmemory|pgvector|sqlitevec|tcvector|elasticsearch|milvus)")
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

	storeType := util.VectorStoreType(*flagStoreType)
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

	httpServer := &http.Server{Addr: *flagAddr}
	server := mcp.NewServer(
		serverName,
		serverVersion,
		mcp.WithServerPath(*flagPath),
		mcp.WithCustomServer(httpServer),
		mcp.WithServerLogger(mcp.GetDefaultLogger()),
	)

	mcpTool := newCodeSearchMCPTool(decl)

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
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown MCP server: %w", err)
		}
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("MCP server exited after shutdown: %w", err)
		}
		return nil
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

func newCodeSearchMCPTool(decl *agenttool.Declaration) *mcp.Tool {
	return mcp.NewTool(
		decl.Name,
		mcp.WithDescription(decl.Description),
		withInputSchema(codeSearchInputSchema()),
	)
}

func withInputSchema(schema *openapi3.Schema) mcp.ToolOption {
	return func(t *mcp.Tool) {
		t.InputSchema = schema
	}
}

func codeSearchInputSchema() *openapi3.Schema {
	return &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"query":  schemaRef(stringSchema(codeSearchQueryDescription)),
			"filter": schemaRef(codeSearchFilterConditionSchema(filterSchemaDepth)),
		},
		AdditionalProperties: disallowAdditionalProperties(),
	}
}

func codeSearchFilterConditionSchema(depth int) *openapi3.Schema {
	return &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: codeSearchFilterDescription,
		Properties: openapi3.Schemas{
			"field": schemaRef(stringSchema(codeSearchFilterFieldDescription)),
			"operator": schemaRef(&openapi3.Schema{
				Type:        &openapi3.Types{openapi3.TypeString},
				Description: codeSearchFilterOperatorDescription,
				Enum:        codeSearchOperatorEnum(),
			}),
			"value": schemaRef(codeSearchFilterValueSchema(depth)),
		},
		AdditionalProperties: disallowAdditionalProperties(),
	}
}

func codeSearchFilterValueSchema(depth int) *openapi3.Schema {
	return &openapi3.Schema{
		Description: codeSearchFilterValueDescription,
		AnyOf:       codeSearchFilterValueAnyOf(depth),
	}
}

func codeSearchFilterValueAnyOf(depth int) openapi3.SchemaRefs {
	return openapi3.SchemaRefs{
		schemaRef(stringSchema("")),
		schemaRef(numberSchema(openapi3.TypeNumber)),
		schemaRef(numberSchema(openapi3.TypeInteger)),
		schemaRef(booleanSchema()),
		schemaRef(objectSchema()),
		schemaRef(arraySchema(codeSearchFilterArrayItemSchema(depth))),
	}
}

func codeSearchFilterArrayItemSchema(depth int) *openapi3.Schema {
	itemAnyOf := openapi3.SchemaRefs{
		schemaRef(stringSchema("")),
		schemaRef(numberSchema(openapi3.TypeNumber)),
		schemaRef(numberSchema(openapi3.TypeInteger)),
		schemaRef(booleanSchema()),
		schemaRef(objectSchema()),
	}
	if depth > 0 {
		itemAnyOf = append(itemAnyOf, schemaRef(codeSearchFilterConditionSchema(depth-1)))
	}
	return &openapi3.Schema{
		AnyOf: itemAnyOf,
	}
}

func codeSearchOperatorEnum() []any {
	return []any{
		searchfilter.OperatorEqual,
		searchfilter.OperatorNotEqual,
		searchfilter.OperatorGreaterThan,
		searchfilter.OperatorGreaterThanOrEqual,
		searchfilter.OperatorLessThan,
		searchfilter.OperatorLessThanOrEqual,
		searchfilter.OperatorIn,
		searchfilter.OperatorNotIn,
		searchfilter.OperatorLike,
		searchfilter.OperatorNotLike,
		searchfilter.OperatorBetween,
		searchfilter.OperatorAnd,
		searchfilter.OperatorOr,
	}
}

func schemaRef(schema *openapi3.Schema) *openapi3.SchemaRef {
	return openapi3.NewSchemaRef("", schema)
}

func stringSchema(description string) *openapi3.Schema {
	return &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: description,
	}
}

func numberSchema(schemaType string) *openapi3.Schema {
	return &openapi3.Schema{
		Type: &openapi3.Types{schemaType},
	}
}

func booleanSchema() *openapi3.Schema {
	return &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeBoolean},
	}
}

func objectSchema() *openapi3.Schema {
	return &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
	}
}

func arraySchema(items *openapi3.Schema) *openapi3.Schema {
	return &openapi3.Schema{
		Type:  &openapi3.Types{openapi3.TypeArray},
		Items: schemaRef(items),
	}
}

func disallowAdditionalProperties() openapi3.AdditionalProperties {
	falseValue := false
	return openapi3.AdditionalProperties{Has: &falseValue}
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
