//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides utility functions.
package util

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/milvus"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// VectorStoreType defines the type of vector store.
type VectorStoreType string

// Vector store type constants.
const (
	VectorStoreInMemory      VectorStoreType = "inmemory"
	VectorStorePGVector      VectorStoreType = "pgvector"
	VectorStoreTCVector      VectorStoreType = "tcvector"
	VectorStoreElasticsearch VectorStoreType = "elasticsearch"
	VectorStoreMilvus        VectorStoreType = "milvus"
)

// NewVectorStoreByType creates a vector store based on the specified type.
func NewVectorStoreByType(storeType VectorStoreType) (vectorstore.VectorStore, error) {
	switch storeType {
	case VectorStorePGVector:
		return newPGVectorStore()
	case VectorStoreTCVector:
		return newTCVectorStore()
	case VectorStoreElasticsearch:
		return newElasticsearchStore()
	case VectorStoreMilvus:
		return newMilvusStore()
	case VectorStoreInMemory:
		fallthrough
	default:
		return inmemory.New(), nil
	}
}

func newPGVectorStore() (vectorstore.VectorStore, error) {
	host := GetEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	portStr := GetEnvOrDefault("PGVECTOR_PORT", "5432")
	port, _ := strconv.Atoi(portStr)
	user := GetEnvOrDefault("PGVECTOR_USER", "root")
	password := GetEnvOrDefault("PGVECTOR_PASSWORD", "")
	database := GetEnvOrDefault("PGVECTOR_DATABASE", "vectordb")
	table := GetEnvOrDefault("PGVECTOR_TABLE", "trpc_agent_go")

	encodedUser := url.QueryEscape(user)
	encodedPassword := url.QueryEscape(password)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		encodedUser, encodedPassword, host, port, database)

	return pgvector.New(
		pgvector.WithPGVectorClientDSN(dsn),
		pgvector.WithTable(table),
	)
}

func newTCVectorStore() (vectorstore.VectorStore, error) {
	url := GetEnvOrDefault("TCVECTOR_URL", "")
	username := GetEnvOrDefault("TCVECTOR_USERNAME", "")
	password := GetEnvOrDefault("TCVECTOR_PASSWORD", "")
	collection := GetEnvOrDefault("TCVECTOR_COLLECTION", "")

	if url == "" || username == "" || password == "" {
		return nil, fmt.Errorf("TCVECTOR_URL, TCVECTOR_USERNAME, and TCVECTOR_PASSWORD are required")
	}

	return tcvector.New(
		tcvector.WithURL(url),
		tcvector.WithUsername(username),
		tcvector.WithPassword(password),
		tcvector.WithCollection(collection),
		tcvector.WithFilterAll(true),
	)
}

func newElasticsearchStore() (vectorstore.VectorStore, error) {
	hosts := GetEnvOrDefault("ELASTICSEARCH_HOSTS", "http://localhost:9200")
	username := GetEnvOrDefault("ELASTICSEARCH_USERNAME", "")
	password := GetEnvOrDefault("ELASTICSEARCH_PASSWORD", "")
	indexName := GetEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_go")
	version := GetEnvOrDefault("ELASTICSEARCH_VERSION", "v8")

	hostList := strings.Split(hosts, ",")
	return elasticsearch.New(
		elasticsearch.WithAddresses(hostList),
		elasticsearch.WithUsername(username),
		elasticsearch.WithPassword(password),
		elasticsearch.WithIndexName(indexName),
		elasticsearch.WithVersion(version),
	)
}

func newMilvusStore() (vectorstore.VectorStore, error) {
	address := GetEnvOrDefault("MILVUS_ADDRESS", "localhost:19530")
	username := GetEnvOrDefault("MILVUS_USERNAME", "")
	password := GetEnvOrDefault("MILVUS_PASSWORD", "")
	dbName := GetEnvOrDefault("MILVUS_DB_NAME", "")
	collection := GetEnvOrDefault("MILVUS_COLLECTION", "trpc_agent_go")

	if address == "" {
		return nil, fmt.Errorf("MILVUS_ADDRESS is required")
	}

	return milvus.New(context.Background(),
		milvus.WithAddress(address),
		milvus.WithUsername(username),
		milvus.WithPassword(password),
		milvus.WithDBName(dbName),
		milvus.WithCollectionName(collection),
	)
}

// WaitForIndexRefresh waits for Elasticsearch index refresh.
// Elasticsearch need a short time to refresh index after index creation or data insertion.
// Milvus also needs a short time to load collection after data insertion.
func WaitForIndexRefresh(storeType VectorStoreType) {
	if storeType == VectorStoreElasticsearch {
		time.Sleep(30 * time.Second)
	}
	if storeType == VectorStoreMilvus {
		time.Sleep(5 * time.Second)
	}
}

// PrintEventWithToolCalls prints the event with tool calls.
func PrintEventWithToolCalls(evt *event.Event) {
	if evt.Error != nil {
		log.Printf("❌ Event error: %v", evt.Error)
		return
	}

	if len(evt.Response.Choices) == 0 {
		return
	}

	choice := evt.Response.Choices[0]

	// Print tool calls
	if len(choice.Message.ToolCalls) > 0 {
		fmt.Println("\n🔧 Tool Calls:")
		for _, tc := range choice.Message.ToolCalls {
			fmt.Printf("  - ID: %s\n", tc.ID)
			fmt.Printf("    Function: %s\n", tc.Function.Name)
			fmt.Printf("    Arguments: %s\n", tc.Function.Arguments)
		}
	}

	// Print tool responses
	if choice.Message.Role == "tool" && choice.Message.Content != "" {
		fmt.Printf("\n📦 Tool Response (Tool Call ID: %s, Tool: %s):\n",
			choice.Message.ToolID, choice.Message.ToolName)
		var toolResult map[string]any
		if err := json.Unmarshal([]byte(choice.Message.Content), &toolResult); err == nil {
			printToolResult(toolResult)
		} else {
			fmt.Printf("%s\n", choice.Message.Content)
		}
	}
}

// printToolResult pretty prints tool responses, focusing on document results.
func printToolResult(toolResult map[string]any) {
	if printDocumentResults(toolResult) {
		return
	}
	if jsonBytes, err := json.MarshalIndent(toolResult, "  ", "  "); err == nil {
		fmt.Printf("%s\n", string(jsonBytes))
	}
}

// printDocumentResults renders document arrays in a compact, readable form.
func printDocumentResults(toolResult map[string]any) bool {
	rawDocs, ok := toolResult["documents"]
	if !ok {
		return false
	}

	docList, ok := rawDocs.([]any)
	if !ok || len(docList) == 0 {
		return false
	}

	fmt.Println("Documents:")
	printed := false
	for idx, rawDoc := range docList {
		docMap, ok := rawDoc.(map[string]any)
		if !ok {
			continue
		}
		text := normalizeText(docMap["text"], 240)
		score, hasScore := normalizeScore(docMap["score"])
		metadata := formatMetadata(docMap["metadata"])

		fmt.Printf("  #%d", idx+1)
		if hasScore {
			fmt.Printf(" score=%.3f", score)
		}
		if metaMap, ok := docMap["metadata"].(map[string]any); ok {
			if denseScore, ok := metaMap[source.MetadataDenseScore]; ok {
				fmt.Printf(" dense=%v", denseScore)
			}
			if sparseScore, ok := metaMap[source.MetadataSparseScore]; ok {
				fmt.Printf(" sparse=%v", sparseScore)
			}
		}
		fmt.Println()

		if text != "" {
			fmt.Printf("    text: %s\n", text)
		}
		if metadata != "" {
			fmt.Printf("    meta: %s\n", metadata)
		}
		printed = true
	}

	if !printed {
		return false
	}

	if message, ok := toolResult["message"].(string); ok && strings.TrimSpace(message) != "" {
		fmt.Printf("Message: %s\n", strings.TrimSpace(message))
	}
	return true
}

// normalizeText trims, flattens, and truncates text for display.
func normalizeText(value any, limit int) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return text
}

// normalizeScore converts score-like values to float64.
func normalizeScore(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// formatMetadata prints metadata as sorted key-value pairs.
func formatMetadata(value any) string {
	metaMap, ok := value.(map[string]any)
	if !ok || len(metaMap) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metaMap))
	for k := range metaMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, metaMap[k]))
	}
	return strings.Join(parts, ", ")
}

// GetEnvOrDefault retrieves the value of an environment variable or returns a default value if not set.
func GetEnvOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

// ExampleDataPath returns the absolute path to example data files.
// relativePath is relative to the exampledata directory.
func ExampleDataPath(relativePath string) string {
	// Get the directory of this source file (util.go)
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return relativePath
	}
	baseDir := filepath.Dir(currentFile)
	return filepath.Join(baseDir, "exampledata", relativePath)
}

// ScoredDoc represents a document with its score for ranking comparison.
type ScoredDoc struct {
	Index int
	Score float64
	Text  string
}

// CalculateEmbeddingScores calculates cosine similarity scores between query and documents.
func CalculateEmbeddingScores(ctx context.Context, query string, documents []string, emb embedder.Embedder) []float64 {
	queryEmbedding, err := emb.GetEmbedding(ctx, query)
	if err != nil {
		log.Fatalf("Failed to get query embedding: %v", err)
	}

	scores := make([]float64, len(documents))
	for i, doc := range documents {
		docEmbedding, err := emb.GetEmbedding(ctx, doc)
		if err != nil {
			log.Printf("Failed to get embedding for doc %d: %v", i, err)
			continue
		}
		scores[i] = CosineSimilarity(queryEmbedding, docEmbedding)
	}

	return scores
}

// NewOpenAIEmbedder creates a new OpenAI embedder with the specified model.
func NewOpenAIEmbedder(model string) embedder.Embedder {
	return openai.New(openai.WithModel(model))
}

// CosineSimilarity calculates cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// PrintEmbeddingResults prints embedding scores sorted by score descending.
func PrintEmbeddingResults(scores []float64, documents []string) {
	docs := make([]ScoredDoc, len(documents))
	for i, doc := range documents {
		docs[i] = ScoredDoc{Index: i, Score: scores[i], Text: doc}
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Score > docs[j].Score
	})

	for rank, doc := range docs {
		fmt.Printf("%d. [Score: %.7f] %s\n", rank+1, doc.Score, doc.Text)
	}
}
