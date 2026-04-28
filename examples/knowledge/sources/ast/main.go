//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates AST-aware ingestion through repo source on a
// mixed-language repository.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
)

var (
	vectorStore  = flag.String("vectorstore", "inmemory", "Vector store type: inmemory|pgvector|sqlitevec|tcvector|elasticsearch|milvus")
	dumpDir      = flag.String("dumpdir", "chunked", "Output directory for parsed documents; defaults to ./chunked and preserves source file structure under go-reader/proto-reader/repo-source")
	goRepoURL    = flag.String("gorepo", "https://github.com/trpc-group/trpc-go", "Remote Go repository URL used for Go reader and repo source demo")
	embedderMode = flag.String("embedder", "mock", "Embedder mode: auto|mock|openai. mock is useful for chunk preview and local validation without real embeddings")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	goRepo := *goRepoURL
	protoRepo := util.ExampleDataPath("ast/proto-lib")

	fmt.Println("🔮 AST Source Demo (Repo Source)")
	fmt.Println("================================")
	fmt.Printf("Go repository URL: %s\n", goRepo)
	fmt.Printf("Proto repository root: %s\n", protoRepo)

	fmt.Println("\n📦 Step 1: Repo source preview on a repository")
	fmt.Println("----------------------------------------------")
	demonstrateRepoSource(ctx, goRepo, protoRepo)
}

func demonstrateRepoSource(ctx context.Context, goRepoURL string, protoRepo string) {
	src := repo.New(
		nil,
		repo.WithRepository(
			repo.Repository{URL: goRepoURL, Branch: "main"},
		),
		repo.WithName("AST Repository"),
		repo.WithFileExtensions([]string{".go", ".md"}),
	)
	protoSrc := dir.New(
		[]string{protoRepo},
		dir.WithName("AST Proto Source"),
		dir.WithRecursive(true),
		dir.WithFileExtensions([]string{".proto"}),
	)

	parseStart := time.Now()
	docs, err := src.ReadDocuments(ctx)
	parseDuration := time.Since(parseStart)
	if err != nil {
		log.Printf("Repo source failed: %v", err)
		return
	}

	fmt.Printf("✓ Repo source parsed %d documents from repository\n", len(docs))
	fmt.Printf("✓ Repo source parse time: %s\n", parseDuration.Round(time.Millisecond))
	fmt.Println("✓ This source path covers Go AST extraction and markdown/document ingestion")
	fmt.Println("✓ Proto AST is loaded via an additional directory source")
	printDocumentPreview(docs, 8)
	if err := dumpDocuments("repo-source", "", docs); err != nil {
		log.Printf("dump repo source documents failed: %v", err)
	} else if *dumpDir != "" {
		fmt.Printf("✓ Repo source results dumped to %s\n", filepath.Join(*dumpDir, "repo-source"))
	}

	apiKey := util.GetEnvOrDefault("OPENAI_API_KEY", "")
	emb, embMode, err := chooseEmbedder(*embedderMode, apiKey)
	if err != nil {
		log.Printf("choose embedder failed: %v", err)
		return
	}

	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Printf("create vector store failed: %v", err)
		return
	}

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources([]source.Source{src, protoSrc}),
	)

	if embMode == "mock" {
		fmt.Println("⚠️  Using MOCK embedder. Similarity search quality is not reliable, but AST parsing, metadata extraction, repo loading, and chunk preview output are unaffected.")
	} else {
		fmt.Println("✓ Using OpenAI embedder")
	}
	fmt.Printf("✓ Loading mixed-language repository into knowledge base with vector store=%s\n", storeType)
	loadStart := time.Now()
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Printf("knowledge.Load failed: %v", err)
		return
	}
	loadDuration := time.Since(loadStart)
	fmt.Println("✓ knowledge.Load completed")
	fmt.Printf("✓ knowledge.Load time: %s\n", loadDuration.Round(time.Millisecond))
}

func printDocumentPreview(docs []*document.Document, limit int) {
	if len(docs) == 0 {
		fmt.Println("(no documents)")
		return
	}

	if limit > len(docs) {
		limit = len(docs)
	}

	for i := 0; i < limit; i++ {
		doc := docs[i]
		fmt.Printf("\n--- Document %d/%d ---\n", i+1, len(docs))
		fmt.Printf("Name: %s\n", doc.Name)
		fmt.Printf("Content Preview: %s\n", shorten(doc.Content, 120))
		fmt.Printf("Embedding Preview: %s\n", shorten(doc.EmbeddingText, 160))
		printMetadataByPrefix(doc.Metadata)
	}
}

func printMetadataByPrefix(metadata map[string]any) {
	var frameworkKeys []string
	var astKeys []string
	var otherKeys []string

	for key := range metadata {
		switch {
		case strings.HasPrefix(key, "trpc_agent_go_"):
			frameworkKeys = append(frameworkKeys, key)
		case strings.HasPrefix(key, "trpc_ast_"):
			astKeys = append(astKeys, key)
		default:
			otherKeys = append(otherKeys, key)
		}
	}

	sort.Strings(frameworkKeys)
	sort.Strings(astKeys)
	sort.Strings(otherKeys)

	if len(frameworkKeys) > 0 {
		fmt.Println("Metadata: trpc_agent_go_*")
		for _, key := range frameworkKeys {
			fmt.Printf("  %s: %v\n", key, metadata[key])
		}
	}

	if len(astKeys) > 0 {
		fmt.Println("Metadata: trpc_ast_*")
		for _, key := range astKeys {
			fmt.Printf("  %s: %v\n", key, metadata[key])
		}
	}

	if len(otherKeys) > 0 {
		fmt.Println("Metadata: other")
		for _, key := range otherKeys {
			fmt.Printf("  %s: %v\n", key, metadata[key])
		}
	}
}

func shorten(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func dumpDocuments(section, root string, docs []*document.Document) error {
	if *dumpDir == "" || len(docs) == 0 {
		return nil
	}

	baseDir := filepath.Join(*dumpDir, section)
	grouped := make(map[string][]*document.Document)
	for _, doc := range docs {
		relPath := relativeSourcePath(root, doc)
		grouped[relPath] = append(grouped[relPath], doc)
	}

	for relPath, groupDocs := range grouped {
		targetDir := filepath.Join(baseDir, filepath.Dir(relPath))
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return err
		}

		sort.SliceStable(groupDocs, func(i, j int) bool {
			left := toInt(groupDocs[i].Metadata["trpc_agent_go_chunk_index"])
			right := toInt(groupDocs[j].Metadata["trpc_agent_go_chunk_index"])
			if left == right {
				return groupDocs[i].Name < groupDocs[j].Name
			}
			return left < right
		})

		fileName := sanitizePathSegment(filepath.Base(relPath)) + ".txt"
		content := renderDocumentGroupDump(relPath, groupDocs)
		if err := os.WriteFile(filepath.Join(targetDir, fileName), []byte(content), 0o644); err != nil {
			return err
		}
	}

	return nil
}

func renderDocumentGroupDump(relPath string, docs []*document.Document) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("source file: %s\n", relPath))
	builder.WriteString(fmt.Sprintf("chunk count: %d\n", len(docs)))
	builder.WriteString("\n")

	for i, doc := range docs {
		builder.WriteString("parsed content:\n")
		builder.WriteString(fmt.Sprintf("index: %d\n", i+1))
		builder.WriteString(fmt.Sprintf("name: %s\n", doc.Name))
		builder.WriteString(fmt.Sprintf("content_length: %d\n", len(doc.Content)))
		builder.WriteString("\ncontent:\n")
		builder.WriteString(doc.Content)
		builder.WriteString("\n")

		if strings.TrimSpace(doc.EmbeddingText) != "" {
			builder.WriteString("\nembedding text:\n")
			builder.WriteString(formatEmbeddingTextForDump(doc.EmbeddingText))
			builder.WriteString("\n")
		}

		builder.WriteString("\nmetadata:\n")
		keys := make([]string, 0, len(doc.Metadata))
		for key := range doc.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString(fmt.Sprintf("%s: %v\n", key, doc.Metadata[key]))
		}
		builder.WriteString("\n-----\n\n")
	}

	return builder.String()
}

func relativeSourcePath(root string, doc *document.Document) string {
	path := toString(doc.Metadata["trpc_ast_file_path"])
	if path == "" {
		path = toString(doc.Metadata["trpc_agent_go_file_path"])
	}
	if path == "" {
		return "unknown"
	}

	if filepath.IsAbs(path) && root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
	}
	return filepath.Clean(path)
}

func sanitizePathSegment(value string) string {
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	return replacer.Replace(value)
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}

func toInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func chooseEmbedder(mode string, apiKey string) (embedder.Embedder, string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		if apiKey != "" {
			return openai.New(), "openai", nil
		}
		return mockEmbedder{}, "mock", nil
	case "mock":
		return mockEmbedder{}, "mock", nil
	case "openai":
		if apiKey == "" {
			return nil, "", fmt.Errorf("embedder=openai requires OPENAI_API_KEY")
		}
		return openai.New(), "openai", nil
	default:
		return nil, "", fmt.Errorf("unknown embedder mode: %s", mode)
	}
}

type mockEmbedder struct{}

func (mockEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	_ = ctx
	base := float64(len([]rune(text)))
	return []float64{base, math.Mod(base, 13), math.Mod(base, 29)}, nil
}

func (m mockEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	vec, err := m.GetEmbedding(ctx, text)
	return vec, map[string]any{"mock": true}, err
}

func (mockEmbedder) GetDimensions() int {
	return 3
}

func formatEmbeddingTextForDump(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
		if pretty, err := json.MarshalIndent(payload, "", "  "); err == nil {
			return string(pretty)
		}
	}

	return trimmed
}
