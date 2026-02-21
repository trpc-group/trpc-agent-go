//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides knowledge base and agent functionality for evaluation.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// searchModeKnowledge wraps a Knowledge instance and forces a specific search mode
// on every Search call. This is used in evaluation to ensure consistent search behavior.
type searchModeKnowledge struct {
	inner      knowledge.Knowledge
	searchMode int
}

func (w *searchModeKnowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	req.SearchMode = w.searchMode
	return w.inner.Search(ctx, req)
}

// VectorStoreType defines the type of vector store.
type VectorStoreType string

// Vector store type constants.
const (
	VectorStoreInMemory VectorStoreType = "inmemory"
	VectorStorePGVector VectorStoreType = "pgvector"
)

// KnowledgeService manages knowledge base operations.
type KnowledgeService struct {
	kb         *knowledge.BuiltinKnowledge
	vs         vectorstore.VectorStore
	emb        embedder.Embedder
	lock       sync.RWMutex
	storeType  VectorStoreType
	modelName  string
	searchMode int // default search mode: 0=hybrid, 1=vector, 2=keyword, 3=filter
}

// NewKnowledgeService creates a new KnowledgeService instance.
// searchMode: 0=hybrid (default), 1=vector, 2=keyword, 3=filter.
func NewKnowledgeService(storeType VectorStoreType, modelName string, searchMode int) (*KnowledgeService, error) {
	svc := &KnowledgeService{
		storeType:  storeType,
		modelName:  modelName,
		searchMode: searchMode,
	}

	var err error
	svc.vs, err = svc.newVectorStoreByType(storeType)
	if err != nil {
		return nil, fmt.Errorf("failed to create vector store: %w", err)
	}

	// Use EMBEDDING_MODEL env var for consistency with Python evaluation
	// Must explicitly pass API key and base URL for venus API compatibility
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	svc.emb = openai.New(
		openai.WithModel("server:274214"),
		openai.WithDimensions(1024),
		openai.WithAPIKey(apiKey),
		openai.WithBaseURL(baseURL),
	)
	svc.kb = knowledge.New(
		knowledge.WithVectorStore(svc.vs),
		knowledge.WithEmbedder(svc.emb),
	)

	return svc, nil
}

func (s *KnowledgeService) newVectorStoreByType(storeType VectorStoreType) (vectorstore.VectorStore, error) {
	switch storeType {
	case VectorStorePGVector:
		return s.newPGVectorStore()
	case VectorStoreInMemory:
		fallthrough
	default:
		return inmemory.New(), nil
	}
}

func (s *KnowledgeService) newPGVectorStore() (vectorstore.VectorStore, error) {
	host := getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	portStr := getEnvOrDefault("PGVECTOR_PORT", "5432")
	port, _ := strconv.Atoi(portStr)
	user := getEnvOrDefault("PGVECTOR_USER", "root")
	password := getEnvOrDefault("PGVECTOR_PASSWORD", "123")
	database := getEnvOrDefault("PGVECTOR_DATABASE", "vector")
	table := getEnvOrDefault("PGVECTOR_TABLE", "trpc_agent_go_eval")

	encodedUser := url.QueryEscape(user)
	encodedPassword := url.QueryEscape(password)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		encodedUser, encodedPassword, host, port, database)

	return pgvector.New(
		pgvector.WithPGVectorClientDSN(dsn),
		pgvector.WithTable(table),
		pgvector.WithIndexDimension(1024), // Match embedding model dimension
		pgvector.WithHybridSearchWeights(0.99999, 0.00001),
	)
}

// Load loads documents from file paths into the knowledge base.
func (s *KnowledgeService) Load(ctx context.Context, filePaths []string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	// Create file source from paths with chunk size=500, overlap=50 (same as LangChain)
	src := file.New(filePaths, file.WithChunkSize(500), file.WithChunkOverlap(50))

	// Recreate knowledge base with new source
	s.kb = knowledge.New(
		knowledge.WithVectorStore(s.vs),
		knowledge.WithEmbedder(s.emb),
		knowledge.WithSources([]source.Source{src}),
		knowledge.WithEnableSourceSync(true),
	)

	// Load documents
	log.Infof("[Load] Starting document loading from %d file(s)...", len(filePaths))
	if err := s.kb.Load(ctx, knowledge.WithShowProgress(true), knowledge.WithDocConcurrency(30), knowledge.WithDocConcurrency(12)); err != nil {
		return fmt.Errorf("failed to load documents: %w", err)
	}

	// Verify loading by checking final count
	finalCount, err := s.vs.Count(ctx)
	if err != nil {
		log.Warnf("[Load] Failed to verify final vector store count: %v", err)
	} else {
		log.Infof("[Load] Vector store count after loading: %d documents", finalCount)
	}

	return nil
}

// DocumentResult represents a single document result with metadata and score.
type DocumentResult struct {
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Score    float64        `json:"score"`
}

// Search searches for relevant documents.
func (s *KnowledgeService) Search(ctx context.Context, query string, k int) ([]*DocumentResult, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	result, err := s.kb.Search(ctx, &knowledge.SearchRequest{
		Query:      query,
		MaxResults: k,
		SearchMode: s.searchMode,
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	var documents []*DocumentResult
	for i, doc := range result.Documents {
		denseScore, hasDenseScore := metadataScore(doc.Document.Metadata, source.MetadataDenseScore)
		sparseScore, hasSparseScore := metadataScore(doc.Document.Metadata, source.MetadataSparseScore)
		log.Infof(
			"[Search] [%d/%d] score=%.4f dense=%s sparse=%s text=%s",
			i+1,
			len(result.Documents),
			doc.Score,
			formatMetadataScore(denseScore, hasDenseScore),
			formatMetadataScore(sparseScore, hasSparseScore),
			truncateForLog(doc.Document.Content, 200),
		)
		documents = append(documents, &DocumentResult{
			Text:     doc.Document.Content,
			Score:    doc.Score,
			Metadata: doc.Document.Metadata,
		})
	}

	return documents, nil
}

// AgentTrace captures intermediate reasoning and tool interactions.
type AgentTrace struct {
	ToolCalls     []ToolCallTrace     `json:"tool_calls,omitempty"`
	ToolResponses []ToolResponseTrace `json:"tool_responses,omitempty"`
	Reasoning     []string            `json:"reasoning,omitempty"`
}

// ToolCallTrace captures a tool call request.
type ToolCallTrace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResponseTrace captures a tool response.
type ToolResponseTrace struct {
	ToolID  string `json:"tool_id"`
	Content string `json:"content"`
}

// AnswerResult contains the final answer and trace information.
type AnswerResult struct {
	Answer   string      `json:"answer"`
	Trace    *AgentTrace `json:"trace,omitempty"`
	Contexts []string    `json:"contexts,omitempty"`
}

// Answer answers a question using RAG with a fresh session.
// Returns answer, documents (contexts from tool responses), trace, and error.
func (s *KnowledgeService) Answer(ctx context.Context, question string, k int) (string, []*DocumentResult, *AgentTrace, error) {
	result, err := s.runAgent(ctx, question, k)
	if err != nil {
		return "", nil, nil, err
	}

	// Convert contexts from trace to DocumentResult.
	// Use make() to ensure an empty slice (JSON []) instead of nil (JSON null),
	// preventing Python-side "'NoneType' object is not iterable" errors.
	documents := make([]*DocumentResult, 0, len(result.Contexts))
	for _, c := range result.Contexts {
		documents = append(documents, &DocumentResult{
			Text: c,
		})
	}

	return result.Answer, documents, result.Trace, nil
}

// Tool description matching LangChain's search_knowledge_base tool

func (s *KnowledgeService) runAgent(ctx context.Context, question string, k int) (*AnswerResult, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	// Create search tool with description matching LangChain.
	// Wrap the knowledge base to force the configured search mode,
	// ensuring consistent retrieval behavior during evaluation.
	// var kb knowledge.Knowledge = s.kb
	// if s.searchMode != 0 {
	// 	kb = &searchModeKnowledge{inner: s.kb, searchMode: s.searchMode}
	// }

	toolDescription := "this is a search tool that help search information you need. It's your knowledgebase, you search information by the tool to answer user's question."
	searchTool := knowledgetool.NewKnowledgeSearchTool(
		s.kb,
		knowledgetool.WithMaxResults(k),
		knowledgetool.WithToolDescription(toolDescription),
	)

	// Temperature = 0 to match LangChain configuration
	temperature := float64(0)
	genConfig := model.GenerationConfig{
		Temperature: &temperature,
	}

	agent := llmagent.New(
		"evaluation-assistant",
		llmagent.WithModel(openaimodel.New(s.modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
		llmagent.WithInstruction(
			"You are a helpful assistant that answers questions using a knowledge base search tool.\n\n"+
				"CRITICAL RULES(IMPORTANT !!!):\n"+
				"1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.\n"+
				"2. Answer ONLY using information retrieved from the search tool.\n"+
				"3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.\n"+
				"4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.\n"+
				"5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.\n"+
				"6. Be concise and stick strictly to the facts from the retrieved information.\n"+
				"7. Give only the direct answer.",
		),
		llmagent.WithGenerationConfig(genConfig),
	)

	sessionService := sessioninmemory.NewSessionService()

	r := runner.NewRunner(
		"eval-runner",
		agent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	eventChan, err := r.Run(ctx, "eval-user", "fresh-session", model.NewUserMessage(question))
	if err != nil {
		return nil, fmt.Errorf("runner failed: %w", err)
	}

	result := &AnswerResult{
		Trace: &AgentTrace{},
	}

	var (
		contentBuilder     strings.Builder // Accumulates streaming content
		hasToolCalls       bool            // Whether any tool calls have been made
		processedToolIDs   = make(map[string]bool)
		lastContentWasTool bool // Track if last processed event was tool-related
		reasoningStepCount int  // Counter for reasoning steps
	)

	log.Infof("[Agent] ========== Processing question: %s ==========", question)

	for evt := range eventChan {
		if evt.Error != nil {
			log.Errorf("[Agent] Event error: %v", evt.Error)
			return nil, fmt.Errorf("event error: %v", evt.Error)
		}

		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}

		// Handle tool calls - flush any accumulated content as reasoning first
		if s.isToolCallEvent(evt) {
			// First, extract any content from THIS event before processing tool calls
			// (some LLMs may include reasoning content in the same event as tool calls)
			if content := s.extractContent(evt); content != "" {
				contentBuilder.WriteString(content)
			}

			// Flush all accumulated content as reasoning before tool call
			if contentBuilder.Len() > 0 {
				reasoning := strings.TrimSpace(contentBuilder.String())
				if reasoning != "" {
					reasoningStepCount++
					result.Trace.Reasoning = append(result.Trace.Reasoning, reasoning)
					log.Infof("[Agent] [Reasoning Step %d - Before Tool Call]: %s", reasoningStepCount, truncateForLog(reasoning, 300))
				}
				contentBuilder.Reset()
			}

			s.captureToolCalls(evt, result.Trace)
			hasToolCalls = true
			lastContentWasTool = true
			continue
		}

		// Handle tool responses and extract contexts
		if s.isToolResponseEvent(evt) {
			s.captureToolResponses(evt, result.Trace, processedToolIDs, result)
			lastContentWasTool = true
			continue
		}

		// Handle content (reasoning or final answer)
		content := s.extractContent(evt)
		if content != "" {
			// If we just came from a tool response, log that we're receiving post-tool content
			if lastContentWasTool {
				log.Debugf("[Agent] Receiving content after tool response...")
				lastContentWasTool = false
			}
			contentBuilder.WriteString(content)
		}

		if evt.IsFinalResponse() {
			// Everything accumulated is the final answer
			finalContent := strings.TrimSpace(contentBuilder.String())
			if finalContent == "" && len(evt.Response.Choices) > 0 {
				finalContent = evt.Response.Choices[0].Message.Content
			}

			// If we had tool calls, the final content is the answer based on tool results
			// If no tool calls, it might be reasoning + answer mixed
			if hasToolCalls {
				result.Answer = finalContent
				log.Infof("[Agent] [Final Answer (after tool calls)]: %s", truncateForLog(finalContent, 300))
			} else {
				// No tool calls - content is direct answer (possibly with reasoning)
				result.Answer = finalContent
				log.Infof("[Agent] [Final Answer (direct)]: %s", truncateForLog(finalContent, 300))
			}

			log.Infof("[Agent] ========== Trace Summary ==========")
			log.Infof("[Agent] Tool calls: %d", len(result.Trace.ToolCalls))
			for i, tc := range result.Trace.ToolCalls {
				log.Infof("[Agent]   [%d] %s (ID: %s) Args: %s", i+1, tc.Name, tc.ID, truncateForLog(tc.Arguments, 100))
			}
			log.Infof("[Agent] Tool responses: %d", len(result.Trace.ToolResponses))
			for i, tr := range result.Trace.ToolResponses {
				log.Infof("[Agent]   [%d] ID: %s Content: %s", i+1, tr.ToolID, truncateForLog(tr.Content, 100))
			}
			log.Infof("[Agent] Reasoning steps: %d", len(result.Trace.Reasoning))
			for i, r := range result.Trace.Reasoning {
				log.Infof("[Agent]   [%d]: %s", i+1, truncateForLog(r, 200))
			}
			log.Infof("[Agent] Contexts extracted: %d", len(result.Contexts))
			log.Infof("[Agent] ====================================")
			break
		}
	}

	log.Infof("[Agent] Final answer: %s", result.Answer)

	return result, nil
}

// isToolCallEvent checks if the event contains tool call requests.
func (s *KnowledgeService) isToolCallEvent(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	choice := evt.Response.Choices[0]
	return len(choice.Message.ToolCalls) > 0 || len(choice.Delta.ToolCalls) > 0
}

// isToolResponseEvent checks if the event contains tool response.
func (s *KnowledgeService) isToolResponseEvent(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool || choice.Message.ToolID != "" {
			return true
		}
	}
	return false
}

// captureToolCalls captures tool call information from the event.
func (s *KnowledgeService) captureToolCalls(evt *event.Event, trace *AgentTrace) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}

	choice := evt.Response.Choices[0]
	toolCalls := choice.Message.ToolCalls
	if len(toolCalls) == 0 {
		toolCalls = choice.Delta.ToolCalls
	}

	for _, toolCall := range toolCalls {
		tc := ToolCallTrace{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: string(toolCall.Function.Arguments),
		}
		trace.ToolCalls = append(trace.ToolCalls, tc)
		log.Infof("[Agent] [Tool Call] %s (ID: %s) Args: %s", tc.Name, tc.ID, truncateForLog(tc.Arguments, 200))
	}
}

// knowledgeSearchResponse matches the format returned by KnowledgeSearchTool.
type knowledgeSearchResponse struct {
	Documents []struct {
		Text     string         `json:"text"`
		Score    float64        `json:"score"`
		Metadata map[string]any `json:"metadata,omitempty"`
	} `json:"documents"`
	Message string `json:"message,omitempty"`
}

// captureToolResponses captures tool response information from the event.
func (s *KnowledgeService) captureToolResponses(evt *event.Event, trace *AgentTrace, processedToolIDs map[string]bool, result *AnswerResult) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}

	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			if processedToolIDs[choice.Message.ToolID] {
				continue
			}
			processedToolIDs[choice.Message.ToolID] = true

			content := strings.TrimSpace(choice.Message.Content)
			tr := ToolResponseTrace{
				ToolID:  choice.Message.ToolID,
				Content: content,
			}
			trace.ToolResponses = append(trace.ToolResponses, tr)

			// Try to parse as KnowledgeSearchResponse to extract individual document texts
			var searchResp knowledgeSearchResponse
			if err := json.Unmarshal([]byte(content), &searchResp); err == nil && len(searchResp.Documents) > 0 {
				// Successfully parsed - extract each document's text as a separate context
				for _, doc := range searchResp.Documents {
					if doc.Text != "" {
						result.Contexts = append(result.Contexts, doc.Text)
						denseScore, hasDenseScore := metadataScore(doc.Metadata, source.MetadataDenseScore)
						sparseScore, hasSparseScore := metadataScore(doc.Metadata, source.MetadataSparseScore)
						log.Infof(
							"[Agent] [Context Extracted] (score=%.4f dense=%s sparse=%s): %s",
							doc.Score,
							formatMetadataScore(denseScore, hasDenseScore),
							formatMetadataScore(sparseScore, hasSparseScore),
							truncateForLog(doc.Text, 200),
						)
					}
				}
			} else {
				// Failed to parse as JSON - use raw content as context
				result.Contexts = append(result.Contexts, content)
				log.Warnf("[Agent] [Tool Response] Could not parse as KnowledgeSearchResponse, using raw content")
			}

			log.Infof("[Agent] [Tool Response] (ID: %s): %s", tr.ToolID, truncateForLog(content, 300))
		}
	}
}

// extractContent extracts content from an event (handles both streaming delta and full message).
func (s *KnowledgeService) extractContent(evt *event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}

	choice := evt.Response.Choices[0]

	// Skip tool role messages
	if choice.Message.Role == model.RoleTool {
		return ""
	}

	// Extract content from delta (streaming) or message
	content := choice.Delta.Content
	if content == "" {
		content = choice.Message.Content
	}

	return content
}

func truncateForLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func metadataScore(metadata map[string]any, key string) (float64, bool) {
	if metadata == nil {
		return 0, false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return 0, false
	}
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
		parsed, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func formatMetadataScore(score float64, hasScore bool) string {
	if !hasScore {
		return "N/A"
	}
	return fmt.Sprintf("%.4f", score)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
