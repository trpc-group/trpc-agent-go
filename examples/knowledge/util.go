package util

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// VectorStoreType defines the type of vector store.
type VectorStoreType string

const (
	VectorStoreInMemory      VectorStoreType = "inmemory"
	VectorStorePGVector      VectorStoreType = "pgvector"
	VectorStoreTCVector      VectorStoreType = "tcvector"
	VectorStoreElasticsearch VectorStoreType = "elasticsearch"
)

// NewVectorStore creates a vector store based on the type.
// Environment variables:
//   - VECTOR_STORE_TYPE: inmemory, pgvector, tcvector, elasticsearch (default: inmemory)
//   - PGVECTOR_HOST, PGVECTOR_PORT, PGVECTOR_USER, PGVECTOR_PASSWORD, PGVECTOR_DATABASE
//   - TCVECTOR_URL, TCVECTOR_USERNAME, TCVECTOR_PASSWORD
//   - ELASTICSEARCH_HOSTS, ELASTICSEARCH_USERNAME, ELASTICSEARCH_PASSWORD, ELASTICSEARCH_INDEX_NAME, ELASTICSEARCH_VERSION
func NewVectorStore() (vectorstore.VectorStore, error) {
	storeType := VectorStoreType(GetEnvOrDefault("VECTOR_STORE_TYPE", "inmemory"))
	return NewVectorStoreByType(storeType)
}

// NewVectorStoreByType creates a vector store based on the specified type.
func NewVectorStoreByType(storeType VectorStoreType) (vectorstore.VectorStore, error) {
	switch storeType {
	case VectorStorePGVector:
		return newPGVectorStore()
	case VectorStoreTCVector:
		return newTCVectorStore()
	case VectorStoreElasticsearch:
		return newElasticsearchStore()
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

	return pgvector.New(
		pgvector.WithHost(host),
		pgvector.WithPort(port),
		pgvector.WithUser(user),
		pgvector.WithPassword(password),
		pgvector.WithDatabase(database),
	)
}

func newTCVectorStore() (vectorstore.VectorStore, error) {
	url := GetEnvOrDefault("TCVECTOR_URL", "")
	username := GetEnvOrDefault("TCVECTOR_USERNAME", "")
	password := GetEnvOrDefault("TCVECTOR_PASSWORD", "")

	if url == "" || username == "" || password == "" {
		return nil, fmt.Errorf("TCVECTOR_URL, TCVECTOR_USERNAME, and TCVECTOR_PASSWORD are required")
	}

	return tcvector.New(
		tcvector.WithURL(url),
		tcvector.WithUsername(username),
		tcvector.WithPassword(password),
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
			if jsonBytes, err := json.MarshalIndent(toolResult, "  ", "  "); err == nil {
				fmt.Printf("%s\n", string(jsonBytes))
			}
		} else {
			fmt.Printf("%s\n", choice.Message.Content)
		}
	}
}

func GetEnvOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}
