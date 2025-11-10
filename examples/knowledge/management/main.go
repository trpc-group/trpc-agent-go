//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates knowledge management with interactive console
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/log"

	// Embedder.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

	// Source.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"

	// Vector store.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"

	// Import PDF reader to register it (optional - comment out if PDF support is not needed).
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

// Command line flags
var (
	embedderType = flag.String("embedder", "openai", "Embedder type: openai")
	vectorStore  = flag.String("vectorstore", "inmemory", "Vector store type: inmemory/pgvector/tcvector/elasticsearch")
	sourceSync   = flag.Bool("source_sync", true, "Enable source sync for incremental sync")
)

// Default values for configurations
const (
	defaultEmbeddingModel = "text-embedding-3-small"
)

// Environment variables
var (
	openaiEmbeddingModel = getEnvOrDefault("OPENAI_EMBEDDING_MODEL", defaultEmbeddingModel)
	// PGVector.
	pgvectorHost     = getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	pgvectorPort     = getEnvOrDefault("PGVECTOR_PORT", "5432")
	pgvectorUser     = getEnvOrDefault("PGVECTOR_USER", "root")
	pgvectorPassword = getEnvOrDefault("PGVECTOR_PASSWORD", "")
	pgvectorDatabase = getEnvOrDefault("PGVECTOR_DATABASE", "vectordb")

	// TCVector.
	tcvectorURL      = getEnvOrDefault("TCVECTOR_URL", "")
	tcvectorUsername = getEnvOrDefault("TCVECTOR_USERNAME", "")
	tcvectorPassword = getEnvOrDefault("TCVECTOR_PASSWORD", "")

	// Elasticsearch.
	elasticsearchHosts     = getEnvOrDefault("ELASTICSEARCH_HOSTS", "http://localhost:9200")
	elasticsearchUsername  = getEnvOrDefault("ELASTICSEARCH_USERNAME", "elastic")
	elasticsearchPassword  = getEnvOrDefault("ELASTICSEARCH_PASSWORD", "")
	elasticsearchAPIKey    = getEnvOrDefault("ELASTICSEARCH_API_KEY", "")
	elasticsearchIndexName = getEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_documents")
	esVersion              = getEnvOrDefault("ELASTICSEARCH_VERSION", "v8")
)

// knowledgeChat manages knowledge base and chat functionality
type knowledgeChat struct {
	source      []source.Source
	knowledge   *knowledge.BuiltinKnowledge
	vectorStore vectorstore.VectorStore // Store reference to vector store
	ctx         context.Context
}

func main() {
	log.SetLevel(log.LevelDebug)
	flag.Parse()

	// Create and run the knowledge chat
	chat := &knowledgeChat{}

	if err := chat.run(); err != nil {
		log.Fatalf("Knowledge chat failed: %v", err)
	}
}

// run runs the console
func (chat *knowledgeChat) run() error {
	if err := chat.setupKnowledgeBase(); err != nil {
		return fmt.Errorf("Failed to setup knowledge base: %v", err)
	}

	fmt.Printf("Knowledge base initialized (Embedder: %s, Vector Store: %s)\n",
		*embedderType, *vectorStore)

	// Auto-load and display default sources
	fmt.Println("\n=== Auto-loading default sources ===")
	if err := chat.knowledge.Load(chat.ctx,
		knowledge.WithShowProgress(true),
		knowledge.WithShowStats(true),
		knowledge.WithSourceConcurrency(2)); err != nil {
		log.Warnf("Warning: Failed to auto-load sources: %v", err)
	} else {
		fmt.Println("Default sources loaded successfully!")

		// Show current sources information
		chat.showCurrentSources()

		fmt.Println("\nNote: Use option 4 to reload all sources or option 6 to view current status")
	}

	return chat.runInteractiveConsole()
}

// runInteractiveConsole starts interactive console
func (chat *knowledgeChat) runInteractiveConsole() error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Println("╔════════════════════════════════════╗")
		fmt.Println("║  Knowledge Base Management System  ║")
		fmt.Println("╠════════════════════════════════════╣")
		fmt.Println("║  1. Add Source (AddSource)         ║")
		fmt.Println("║  2. Remove Source (RemoveSource)   ║")
		fmt.Println("║  3. Reload Source (ReloadSource)   ║")
		fmt.Println("║  4. Load All Sources (Load)        ║")
		fmt.Println("║  5. Search Knowledge (Search)      ║")
		fmt.Println("║  6. Show Current Sources           ║")
		fmt.Println("║  7. Exit                           ║")
		fmt.Println("╚════════════════════════════════════╝")
		fmt.Print("\n➤ Select operation (1-7): ")
		scanner.Scan()
		choice := strings.TrimSpace(scanner.Text())

		fmt.Println("\n" + strings.Repeat("─", 50))
		switch choice {
		case "1":
			chat.addSourceMenu(scanner)
		case "2":
			chat.removeSourceMenu(scanner)
		case "3":
			chat.reloadSourceMenu(scanner)
		case "4":
			chat.loadAllSources(scanner)
		case "5":
			chat.searchMenu(scanner)
		case "6":
			chat.showCurrentSources()
		case "7":
			return nil
		default:
			fmt.Println("Invalid choice, please try again")
		}
		fmt.Println(strings.Repeat("─", 50))
		fmt.Println("Operation completed")
		fmt.Println(strings.Repeat("═", 50))
		fmt.Println()
	}
}

// addSourceMenu handles adding source
func (chat *knowledgeChat) addSourceMenu(scanner *bufio.Scanner) {
	fmt.Print("Enter source name: ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())

	fmt.Print("Enter file path (relative to data/ folder): ")
	scanner.Scan()
	filePath := strings.TrimSpace(scanner.Text())

	// Get metadata from user
	fmt.Print("Enter metadata (key1 value1 key2 value2 ... or press enter to skip): ")
	scanner.Scan()
	metadataInput := strings.TrimSpace(scanner.Text())

	// Parse metadata
	metadata := make(map[string]any)
	if metadataInput != "" {
		parts := strings.Fields(metadataInput)
		if len(parts)%2 != 0 {
			fmt.Println("Warning: Metadata should be in key-value pairs, ignoring last key")
		}
		for i := 0; i < len(parts)-1; i += 2 {
			key := parts[i]
			value := parts[i+1]
			metadata[key] = value
		}
	}

	// Create file source with name and metadata
	options := []file.Option{file.WithName(name)}
	if len(metadata) > 0 {
		options = append(options, file.WithMetadata(metadata))
	}
	fsSource := file.New([]string{filePath}, options...)

	// Add to knowledge base
	if err := chat.knowledge.AddSource(chat.ctx, fsSource,
		knowledge.WithShowProgress(true),
		knowledge.WithShowStats(true)); err != nil {
		log.Warnf("Failed to add source: %v", err)
	} else {
		fmt.Printf("Successfully added source: %s", name)
		if len(metadata) > 0 {
			fmt.Printf(" with metadata: %v", metadata)
		}
		fmt.Println()
	}
}

// removeSourceMenu handles removing source
func (chat *knowledgeChat) removeSourceMenu(scanner *bufio.Scanner) {
	fmt.Print("Enter source name to remove: ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())

	if err := chat.knowledge.RemoveSource(chat.ctx, name); err != nil {
		log.Warnf("Failed to remove source: %v", err)
	} else {
		fmt.Printf("Successfully removed source: %s\n", name)
	}
}

// reloadSourceMenu handles reloading source
func (chat *knowledgeChat) reloadSourceMenu(scanner *bufio.Scanner) {
	fmt.Print("Enter source name to reload: ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())

	// Find existing source - in a real implementation you would track sources
	// For demonstration, we'll create a new source with same path

	fmt.Print("Enter file path for reload: ")
	scanner.Scan()
	filePath := strings.TrimSpace(scanner.Text())

	fsSource := file.New([]string{filePath}, file.WithName(name))

	if err := chat.knowledge.ReloadSource(chat.ctx, fsSource,
		knowledge.WithShowProgress(true),
		knowledge.WithShowStats(true)); err != nil {
		log.Warnf("Failed to reload source: %v", err)
	} else {
		fmt.Printf("Successfully reloaded source: %s\n", name)
	}
}

// loadAllSources loads all sources
func (chat *knowledgeChat) loadAllSources(scanner *bufio.Scanner) {
	recreate := false
	fmt.Println("Loading all sources...")
	fmt.Println("Do you want to recreate the knowledge base? (y/n)")
	scanner.Scan()
	recreateText := strings.TrimSpace(scanner.Text())
	if recreateText == "y" {
		recreate = true
		fmt.Println("Recreating knowledge base...")
	}
	if err := chat.knowledge.Load(chat.ctx,
		knowledge.WithShowProgress(true),
		knowledge.WithShowStats(true),
		knowledge.WithSourceConcurrency(2),
		knowledge.WithRecreate(recreate)); err != nil {
		log.Warnf("Failed to load sources: %v", err)
	} else {
		fmt.Println("All sources loaded successfully!")
	}
}

// searchMenu handles search
func (chat *knowledgeChat) searchMenu(scanner *bufio.Scanner) {
	fmt.Print("Enter search query: ")
	scanner.Scan()
	query := strings.TrimSpace(scanner.Text())

	if query == "" {
		fmt.Println("Search query cannot be empty")
		return
	}

	fmt.Print("Enter max results (default 1): ")
	scanner.Scan()
	limitStr := strings.TrimSpace(scanner.Text())
	limit := 1
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Execute search
	result, err := chat.knowledge.Search(chat.ctx, &knowledge.SearchRequest{
		Query:      query,
		MaxResults: limit,
	})

	if err != nil {
		log.Warnf("Search failed: %v", err)
		return
	}

	fmt.Printf("\n=== Search Results ===\n")
	fmt.Printf("Score: %.3f\n", result.Score)
	fmt.Printf("Content: %s\n", result.Text)
	if result.Document != nil && result.Document.Metadata != nil {
		if sourceName, ok := result.Document.Metadata[source.MetaSourceName].(string); ok {
			fmt.Printf("Source: %s\n", sourceName)
		}
	}
}

// showCurrentSources shows current sources with metadata and counts using ShowDocumentInfo
func (chat *knowledgeChat) showCurrentSources() {
	fmt.Println("\n=== Current Sources Information ===")

	// Get document info using ShowDocumentInfo method
	docInfos, err := chat.knowledge.ShowDocumentInfo(chat.ctx)
	if err != nil {
		fmt.Printf("Error getting document info: %v\n", err)
		return
	}

	fmt.Printf("Total documents in vector store: %d\n", len(docInfos))

	// Organize document info by source name
	sourceStats := make(map[string]struct {
		uriCounts map[string]int // URI -> document count
		metadata  map[string]any // source-level metadata
		uris      []string       // unique URIs for this source
	})

	for _, docInfo := range docInfos {
		sourceName := docInfo.SourceName
		sourceURI := docInfo.URI

		if sourceName != "" && sourceURI != "" {
			// Initialize source entry if not exists
			if _, exists := sourceStats[sourceName]; !exists {
				sourceStats[sourceName] = struct {
					uriCounts map[string]int
					metadata  map[string]any
					uris      []string
				}{
					uriCounts: make(map[string]int),
					metadata:  make(map[string]any),
					uris:      []string{},
				}
			}

			stats := sourceStats[sourceName]

			// Track URI counts
			stats.uriCounts[sourceURI]++

			// Collect unique URIs
			found := false
			for _, uri := range stats.uris {
				if uri == sourceURI {
					found = true
					break
				}
			}
			if !found {
				stats.uris = append(stats.uris, sourceURI)
			}

			// Collect source-level metadata (excluding system fields)
			for key, value := range docInfo.AllMeta {
				if !strings.HasPrefix(key, "trpc_agent_go_") {
					stats.metadata[key] = value
				}
			}

			sourceStats[sourceName] = stats
		}
	}

	// Display sources information
	if len(sourceStats) == 0 {
		fmt.Println("No sources found in vector store")
		return
	}

	for sourceName, stats := range sourceStats {
		totalDocs := 0
		for _, count := range stats.uriCounts {
			totalDocs += count
		}

		fmt.Printf("\nSource Name: %s\n", sourceName)
		fmt.Printf("  Total Documents: %d\n", totalDocs)
		fmt.Printf("  URIs: %d unique URI(s)\n", len(stats.uris))

		// Display URIs and their document counts
		for _, uri := range stats.uris {
			fmt.Printf("    URI: %s (%d documents)\n", uri, stats.uriCounts[uri])
		}

		if len(stats.metadata) > 0 {
			fmt.Printf("  Source Metadata:\n")
			for key, value := range stats.metadata {
				fmt.Printf("    %s: %v\n", key, value)
			}
		} else {
			fmt.Printf("  No source metadata\n")
		}
	}
}

// setupKnowledgeBase sets up knowledge base
func (chat *knowledgeChat) setupKnowledgeBase() error {
	chat.ctx = context.Background()

	// Create embedder
	embedder, err := chat.setupEmbedder(chat.ctx)
	if err != nil {
		return fmt.Errorf("Failed to setup embedder: %v", err)
	}

	vs, err := chat.setupVectorDB()
	if err != nil {
		return fmt.Errorf("Failed to setup vector store: %v", err)
	}

	fileSource1 := file.New(
		[]string{"../exampledata/file/llm.md"},
		file.WithName("LLMDocSource"),
		file.WithMetadata(map[string]any{"tag": "llm"}),
	)
	fileSource2 := file.New(
		[]string{"../exampledata/file/golang.md"},
		file.WithName("GolangDocSource"),
		file.WithMetadata(map[string]any{"tag": "golang"}),
	)
	urlSource1 := url.New(
		[]string{"https://en.wikipedia.org/wiki/Byte-pair_encoding"},
		url.WithName("Byte-pair"),
		url.WithMetadataValue("tag", "wiki"),
	)
	urlSource2 := url.New(
		[]string{"https://trpc-go.com/Byte-pair_encoding"}, // contentFetchURL is configured, this url will be used to generate meta data and docID
		url.WithName("trpc-go"),
		url.WithContentFetchingURL([]string{"https://en.wikipedia.org/wiki/Byte-pair_encoding"}), // real url that fetching data
		url.WithMetadataValue("tag", "wiki"),
	)

	chat.source = []source.Source{fileSource1, fileSource2, urlSource1, urlSource2}
	// Create knowledge base
	chat.knowledge = knowledge.New(
		knowledge.WithEmbedder(embedder),
		knowledge.WithVectorStore(vs),
		knowledge.WithSources([]source.Source{fileSource1, fileSource2, urlSource1, urlSource2}),
		knowledge.WithEnableSourceSync(*sourceSync),
	)

	// Save reference to vector store
	chat.vectorStore = vs

	return nil
}

// setupVectorDB creates the appropriate vector store based on the selected type.
func (chat *knowledgeChat) setupVectorDB() (vectorstore.VectorStore, error) {
	switch strings.ToLower(*vectorStore) {
	case "inmemory":
		return vectorinmemory.New(), nil
	case "pgvector":
		port, err := strconv.Atoi(pgvectorPort)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pgvector port: %w", err)
		}
		vs, err := vectorpgvector.New(
			vectorpgvector.WithHost(pgvectorHost),
			vectorpgvector.WithPort(port),
			vectorpgvector.WithUser(pgvectorUser),
			vectorpgvector.WithPassword(pgvectorPassword),
			vectorpgvector.WithDatabase(pgvectorDatabase),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create pgvector store: %w", err)
		}
		return vs, nil
	case "tcvector":
		vs, err := vectortcvector.New(
			vectortcvector.WithURL(tcvectorURL),
			vectortcvector.WithUsername(tcvectorUsername),
			vectortcvector.WithPassword(tcvectorPassword),
			vectortcvector.WithCollection("tcvector-agent-go"),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create tcvector store: %w", err)
		}
		return vs, nil
	case "elasticsearch":
		// Parse hosts string into slice.
		hosts := strings.Split(elasticsearchHosts, ",")
		for i, host := range hosts {
			hosts[i] = strings.TrimSpace(host)
		}

		vs, err := vectorelasticsearch.New(
			vectorelasticsearch.WithAddresses(hosts),
			vectorelasticsearch.WithUsername(elasticsearchUsername),
			vectorelasticsearch.WithPassword(elasticsearchPassword),
			vectorelasticsearch.WithAPIKey(elasticsearchAPIKey),
			vectorelasticsearch.WithIndexName(elasticsearchIndexName),
			vectorelasticsearch.WithMaxRetries(3),
			vectorelasticsearch.WithVersion(esVersion),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create elasticsearch store: %w", err)
		}
		return vs, nil
	default:
		return nil, fmt.Errorf("unsupported vector store type: %s", *vectorStore)
	}
}

// setupEmbedder creates embedder based on the configured embedderType.
func (chat *knowledgeChat) setupEmbedder(ctx context.Context) (embedder.Embedder, error) {
	switch strings.ToLower(*embedderType) {
	case "gemini":
		return geminiembedder.New(ctx)
	default: // openai
		return openaiembedder.New(
			openaiembedder.WithModel(openaiEmbeddingModel),
		), nil
	}
}

// getEnvOrDefault returns environment variable or default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
