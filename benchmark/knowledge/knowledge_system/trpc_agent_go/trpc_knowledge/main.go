//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides HTTP service for knowledge base evaluation.
// This service implements the KnowledgeBase interface (load, search, answer)
// and exposes them as HTTP endpoints for Python RAGAS evaluation.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-v3.2
//   - PGVECTOR_HOST, PGVECTOR_PORT, PGVECTOR_USER, PGVECTOR_PASSWORD, PGVECTOR_DATABASE: PGVector config
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	go run main.go --port 8080
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

var (
	port           = flag.Int("port", 8765, "HTTP server port")
	vectorStoreArg = flag.String("vectorstore", "pgvector", "Vector store type: inmemory|pgvector")
	searchModeArg  = flag.Int("search-mode", 0, "Search mode: 0=hybrid (default), 1=vector, 2=keyword, 3=filter")
	modelName      = getEnvOrDefault("MODEL_NAME", "deepseek-v3.2")

	// Tunable parameters for vertical evaluation
	hybridVectorWeight = flag.Float64("hybrid-vector-weight", 0.99999, "Hybrid search vector weight (0.0-1.0)")
	hybridTextWeight   = flag.Float64("hybrid-text-weight", 0.00001, "Hybrid search text weight (0.0-1.0)")
	pgTable            = flag.String("pg-table", "", "PGVector table name (overrides PGVECTOR_TABLE env var)")
	useRRF             = flag.Bool("use-rrf", false, "Use Reciprocal Rank Fusion instead of weighted score fusion")
)

// Global knowledge service
var knowledgeSvc *KnowledgeService

// LoadRequest represents the request body for /load endpoint.
type LoadRequest struct {
	FilePaths []string `json:"file_paths"`
}

// LoadResponse represents the response for /load endpoint.
type LoadResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// SearchRequest represents the request body for /search endpoint.
type SearchRequest struct {
	Query string `json:"query"`
	K     int    `json:"k"`
}

// SearchResponse represents the response for /search endpoint.
type SearchResponse struct {
	Documents []*DocumentResult `json:"documents"`
	Message   string            `json:"message,omitempty"`
}

// AnswerRequest represents the request body for /answer endpoint.
type AnswerRequest struct {
	Question string `json:"question"`
	K        int    `json:"k"`
}

// AnswerResponse represents the response for /answer endpoint.
type AnswerResponse struct {
	Answer    string            `json:"answer"`
	Documents []*DocumentResult `json:"documents"`
	Trace     *AgentTrace       `json:"trace,omitempty"`
	Message   string            `json:"message,omitempty"`
}

func main() {
	flag.Parse()

	searchModeNames := map[int]string{0: "hybrid", 1: "vector", 2: "keyword", 3: "filter"}
	fmt.Println("Knowledge Base HTTP Service")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Vector Store: %s\n", *vectorStoreArg)
	fmt.Printf("Search Mode: %s (%d)\n", searchModeNames[*searchModeArg], *searchModeArg)
	fmt.Printf("Use RRF: %v\n", *useRRF)
	if !*useRRF {
		fmt.Printf("Hybrid Weights: vector=%.5f text=%.5f\n", *hybridVectorWeight, *hybridTextWeight)
	}
	if *pgTable != "" {
		fmt.Printf("PG Table: %s (override)\n", *pgTable)
	}
	fmt.Printf("PG Host: %s:%s\n", getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1"), getEnvOrDefault("PGVECTOR_PORT", "5432"))
	fmt.Printf("PG Database: %s (User: %s)\n", getEnvOrDefault("PGVECTOR_DATABASE", "rgb"), getEnvOrDefault("PGVECTOR_USER", "root"))
	apiKey := os.Getenv("OPENAI_API_KEY")
	var maskedKey string
	if len(apiKey) == 0 {
		maskedKey = "(not set)"
	} else if len(apiKey) > 8 {
		maskedKey = apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
	} else {
		maskedKey = "****"
	}
	fmt.Printf("OPENAI_API_KEY: %s\n", maskedKey)
	fmt.Printf("OPENAI_BASE_URL: %s\n", os.Getenv("OPENAI_BASE_URL"))
	fmt.Println(strings.Repeat("=", 50))

	svcConfig := &ServiceConfig{
		StoreType:          VectorStoreType(*vectorStoreArg),
		ModelName:          modelName,
		SearchMode:         *searchModeArg,
		HybridVectorWeight: *hybridVectorWeight,
		HybridTextWeight:   *hybridTextWeight,
		PGTable:            *pgTable,
		UseRRF:             *useRRF,
	}

	var err error
	knowledgeSvc, err = NewKnowledgeServiceWithConfig(svcConfig)
	if err != nil {
		log.Fatalf("Failed to initialize knowledge service: %v", err)
	}

	http.HandleFunc("/load", handleLoad)
	http.HandleFunc("/search", handleSearch)
	http.HandleFunc("/answer", handleAnswer)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/config", handleConfig)

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("Server listening on http://localhost%s\n", addr)
	fmt.Println("\nEndpoints:")
	fmt.Println("  POST /load   - Load documents into knowledge base")
	fmt.Println("  POST /search - Search for relevant documents")
	fmt.Println("  POST /answer - Answer a question using RAG")
	fmt.Println("  GET  /health - Health check")
	fmt.Println("  GET  /config - Current service configuration")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func waitForIndexRefresh() {
	time.Sleep(100 * time.Millisecond)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Collect PG connection info from environment (masking password)
	host := getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	portStr := getEnvOrDefault("PGVECTOR_PORT", "5432")
	user := getEnvOrDefault("PGVECTOR_USER", "root")
	database := getEnvOrDefault("PGVECTOR_DATABASE", "rgb")

	// Resolve the effective table name: command-line flag overrides env var.
	effectiveTable := getEnvOrDefault("PGVECTOR_TABLE", "trpc_agent_go_eval")
	if knowledgeSvc.config.PGTable != "" {
		effectiveTable = knowledgeSvc.config.PGTable
	}

	cfg := map[string]any{
		"model_name":           knowledgeSvc.modelName,
		"vectorstore":          string(knowledgeSvc.storeType),
		"search_mode":          knowledgeSvc.searchMode,
		"use_rrf":              knowledgeSvc.config.UseRRF,
		"hybrid_vector_weight": knowledgeSvc.config.HybridVectorWeight,
		"hybrid_text_weight":   knowledgeSvc.config.HybridTextWeight,
		"pg_table":             effectiveTable,
		"pg_connection": map[string]string{
			"host":     host,
			"port":     portStr,
			"user":     user,
			"database": database,
			// Password intentionally omitted for security
		},
	}
	json.NewEncoder(w).Encode(cfg)
}

func handleLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if len(req.FilePaths) == 0 {
		http.Error(w, "No file paths provided", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	if err := knowledgeSvc.Load(ctx, req.FilePaths); err != nil {
		http.Error(w, fmt.Sprintf("Failed to load documents: %v", err), http.StatusInternalServerError)
		return
	}

	waitForIndexRefresh()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoadResponse{
		Success: true,
		Message: "Documents loaded successfully",
		Count:   len(req.FilePaths),
	})
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}

	if req.K <= 0 {
		req.K = 4
	}

	ctx := context.Background()
	documents, err := knowledgeSvc.Search(ctx, req.Query, req.K)
	if err != nil {
		http.Error(w, fmt.Sprintf("Search failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SearchResponse{
		Documents: documents,
		Message:   fmt.Sprintf("Found %d relevant document(s)", len(documents)),
	})
}

func handleAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Question == "" {
		http.Error(w, "Question is required", http.StatusBadRequest)
		return
	}

	if req.K <= 0 {
		req.K = 4
	}

	ctx := context.Background()
	answer, documents, trace, err := knowledgeSvc.Answer(ctx, req.Question, req.K)
	if err != nil {
		http.Error(w, fmt.Sprintf("Answer failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AnswerResponse{
		Answer:    answer,
		Documents: documents,
		Trace:     trace,
		Message:   fmt.Sprintf("Found %d relevant document(s)", len(documents)),
	})
}
