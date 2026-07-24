//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main serves an interactive local viewer for knowledge chunking
// strategies.
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	chunkingdemo "trpc.group/trpc-go/trpc-agent-go/examples/knowledge/chunking"
)

const (
	maxRequestSize        = 3 << 20
	maxContentSize        = 1 << 20
	maxResponseChunkBytes = 8 << 20
	maxNameRunes          = 255
	minChunkSize          = 32
	minCoreSize           = 16
	maxChunkCount         = 5000
	maxConcurrentRequests = 2
)

var chunkRequestSlots = make(chan struct{}, maxConcurrentRequests)

//go:embed index.html
var indexHTML []byte

type chunkRequest struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Strategy  string `json:"strategy"`
	ChunkSize int    `json:"chunk_size"`
	Overlap   int    `json:"overlap"`
}

type chunkResponse struct {
	Input    inputView    `json:"input"`
	Config   configView   `json:"config"`
	Source   sourceView   `json:"source"`
	Strategy strategyView `json:"strategy"`
}

type inputView struct {
	Name  string `json:"name"`
	Runes int    `json:"runes"`
	Bytes int    `json:"bytes"`
}

type configView struct {
	RequestedStrategy string `json:"requested_strategy"`
	Strategy          string `json:"strategy"`
	Reader            string `json:"reader,omitempty"`
	Automatic         bool   `json:"automatic"`
	ChunkSize         int    `json:"chunk_size"`
	Overlap           int    `json:"overlap"`
	SizeUnit          string `json:"size_unit"`
}

type sourceView struct {
	Content      string `json:"content"`
	Normalized   bool   `json:"normalized"`
	MappedChunks int    `json:"mapped_chunks"`
}

type strategyView struct {
	Name       string      `json:"name"`
	Total      int         `json:"total"`
	MinSize    int         `json:"min_size"`
	MaxSize    int         `json:"max_size"`
	AvgSize    float64     `json:"avg_size"`
	OverBudget int         `json:"over_budget"`
	Chunks     []chunkView `json:"chunks"`
}

type chunkView struct {
	Index         int            `json:"index"`
	ID            string         `json:"id"`
	Content       string         `json:"content"`
	Runes         int            `json:"runes"`
	Bytes         int            `json:"bytes"`
	WithinBudget  bool           `json:"within_budget"`
	ActualOverlap int            `json:"actual_overlap"`
	Metadata      map[string]any `json:"metadata"`
	Mapped        bool           `json:"mapped"`
	SourceStart   int            `json:"source_start"`
	SourceEnd     int            `json:"source_end"`
	OverlapStart  int            `json:"overlap_start"`
}

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", serveIndex)
	mux.HandleFunc("/api/samples", serveSamples)
	mux.HandleFunc("/api/chunks", serveChunks)

	server := &http.Server{
		Addr:              *address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("Chunking viewer: http://%s", *address)
	log.Fatal(server.ListenAndServe())
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func serveSamples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, chunkingdemo.Samples())
}

func serveChunks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request chunkRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, "decode request: "+err.Error())
		return
	}
	if strings.TrimSpace(request.Content) == "" {
		writeError(w, "document content must not be empty")
		return
	}
	if request.Name == "" {
		request.Name = "document"
	}
	if request.Strategy == "json" {
		request.Overlap = 0
	}
	if err := validateRequest(request); err != nil {
		writeError(w, err.Error())
		return
	}

	select {
	case chunkRequestSlots <- struct{}{}:
		defer func() { <-chunkRequestSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeErrorStatus(w, http.StatusTooManyRequests,
			"the viewer is busy; retry shortly")
		return
	}

	results, err := chunkingdemo.Run(
		request.Strategy,
		request.Name,
		request.Content,
		request.ChunkSize,
		request.Overlap,
	)
	if err != nil {
		writeError(w, err.Error())
		return
	}
	if len(results) != 1 {
		writeError(w, "select exactly one chunking strategy")
		return
	}
	if len(results[0].Chunks) > maxChunkCount {
		writeError(w, fmt.Sprintf(
			"chunk count %d exceeds the viewer limit of %d",
			len(results[0].Chunks),
			maxChunkCount,
		))
		return
	}
	totalChunkBytes := 0
	for _, chunk := range results[0].Chunks {
		totalChunkBytes += len(chunk.Content)
	}
	if totalChunkBytes > maxResponseChunkBytes {
		writeError(w, fmt.Sprintf(
			"chunk content exceeds the viewer output limit of %d bytes",
			maxResponseChunkBytes,
		))
		return
	}
	writeJSON(w, http.StatusOK, buildResponse(request, results[0]))
}

func validateRequest(request chunkRequest) error {
	switch request.Strategy {
	case "", "reader", "fixed", "recursive", "markdown", "json":
	default:
		return fmt.Errorf("unsupported viewer strategy %q", request.Strategy)
	}
	if len(request.Content) > maxContentSize {
		return fmt.Errorf(
			"document exceeds the viewer limit of %d bytes",
			maxContentSize,
		)
	}
	if utf8.RuneCountInString(request.Name) > maxNameRunes {
		return fmt.Errorf(
			"document name exceeds the viewer limit of %d runes",
			maxNameRunes,
		)
	}
	if request.ChunkSize > 0 && request.ChunkSize < minChunkSize {
		return fmt.Errorf(
			"chunk size must be 0 or at least %d in the viewer",
			minChunkSize,
		)
	}

	jsonStrategy := request.Strategy == "json" ||
		request.Strategy == "reader" &&
			strings.EqualFold(filepath.Ext(request.Name), ".json")
	effectiveChunkSize := request.ChunkSize
	if effectiveChunkSize == 0 {
		effectiveChunkSize = 1024
		if jsonStrategy {
			effectiveChunkSize = 2000
		}
	}
	coreSize := effectiveChunkSize
	if !jsonStrategy {
		coreSize -= request.Overlap
		if coreSize < minCoreSize {
			return fmt.Errorf(
				"chunk size minus overlap must be at least %d in the viewer",
				minCoreSize,
			)
		}
	}

	units := utf8.RuneCountInString(request.Content)
	if jsonStrategy {
		units = len(request.Content)
	}
	estimatedPayload := max(1, coreSize/2)
	estimatedChunks := (units + estimatedPayload - 1) / estimatedPayload
	if request.Strategy == "markdown" ||
		request.Strategy == "reader" &&
			(strings.EqualFold(filepath.Ext(request.Name), ".md") ||
				strings.EqualFold(filepath.Ext(request.Name), ".markdown")) {
		estimatedChunks = max(
			estimatedChunks,
			strings.Count(request.Content, "\n")+1,
		)
	}
	if jsonStrategy {
		estimatedChunks = max(
			estimatedChunks,
			strings.Count(request.Content, ",")+1,
		)
	}
	if estimatedChunks > maxChunkCount {
		return fmt.Errorf(
			"request may produce about %d chunks, exceeding the viewer limit of %d",
			estimatedChunks,
			maxChunkCount,
		)
	}
	return nil
}

func buildResponse(request chunkRequest, result chunkingdemo.Result) chunkResponse {
	sizeUnit := result.SizeUnit
	sourceContent := request.Content
	if result.Name != "json" {
		sourceContent = normalizeSource(request.Content)
	}
	strategy := strategyView{
		Name:   strategyDisplayName(result.Name),
		Total:  len(result.Chunks),
		Chunks: make([]chunkView, 0, len(result.Chunks)),
	}
	totalSize := 0
	for i, chunk := range result.Chunks {
		runeSize := utf8.RuneCountInString(chunk.Content)
		configuredSize := runeSize
		if sizeUnit == "bytes" {
			configuredSize = len(chunk.Content)
		}
		if i == 0 || configuredSize < strategy.MinSize {
			strategy.MinSize = configuredSize
		}
		strategy.MaxSize = max(strategy.MaxSize, configuredSize)
		totalSize += configuredSize
		if configuredSize > result.ChunkSize {
			strategy.OverBudget++
		}

		actualOverlap := 0
		if i > 0 {
			actualOverlap = chunkingdemo.BoundaryOverlap(
				result.Chunks[i-1].Content,
				chunk.Content,
				result.Overlap,
			)
		}
		strategy.Chunks = append(strategy.Chunks, chunkView{
			Index:         i + 1,
			ID:            chunk.ID,
			Content:       chunk.Content,
			Runes:         runeSize,
			Bytes:         len(chunk.Content),
			WithinBudget:  configuredSize <= result.ChunkSize,
			ActualOverlap: actualOverlap,
			Metadata:      chunk.Metadata,
		})
	}
	if strategy.Total > 0 {
		strategy.AvgSize = float64(totalSize) / float64(strategy.Total)
	}
	mappedChunks := mapSourceRanges(
		sourceContent,
		result.Name,
		strategy.Chunks,
	)

	return chunkResponse{
		Input: inputView{
			Name:  request.Name,
			Runes: utf8.RuneCountInString(request.Content),
			Bytes: len(request.Content),
		},
		Config: configView{
			RequestedStrategy: request.Strategy,
			Strategy:          result.Name,
			Reader:            result.Reader,
			Automatic:         result.Automatic,
			ChunkSize:         result.ChunkSize,
			Overlap:           result.Overlap,
			SizeUnit:          sizeUnit,
		},
		Source: sourceView{
			Content:      sourceContent,
			Normalized:   sourceContent != request.Content,
			MappedChunks: mappedChunks,
		},
		Strategy: strategy,
	}
}

func strategyDisplayName(strategyName string) string {
	switch strategyName {
	case "fixed":
		return "FixedSizeChunking"
	case "recursive":
		return "RecursiveChunking"
	case "markdown":
		return "MarkdownChunking"
	case "json":
		return "JSONChunking"
	default:
		return strategyName
	}
}

func normalizeSource(content string) string {
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

func mapSourceRanges(
	sourceContent string,
	strategyName string,
	chunks []chunkView,
) int {
	if strategyName == "json" {
		return 0
	}

	sourceRunes := []rune(sourceContent)
	cursor := 0
	mappedChunks := 0
	for i := range chunks {
		chunkRunes := []rune(chunks[i].Content)
		coreOffset := min(chunks[i].ActualOverlap, len(chunkRunes))
		if chunks[i].ActualOverlap > 0 {
			for coreOffset < len(chunkRunes) &&
				unicode.IsSpace(chunkRunes[coreOffset]) {
				coreOffset++
			}
		}
		coreRunes := chunkRunes[coreOffset:]
		sourceStart, sourceEnd := findRuneRange(
			sourceRunes,
			coreRunes,
			cursor,
		)
		if sourceStart < 0 {
			continue
		}

		overlapStart := sourceStart
		if chunks[i].ActualOverlap > 0 {
			overlapRunes := chunkRunes[:chunks[i].ActualOverlap]
			searchStart := 0
			if i > 0 && chunks[i-1].Mapped {
				searchStart = chunks[i-1].SourceStart
			}
			if position := findLastRunes(
				sourceRunes,
				overlapRunes,
				searchStart,
				sourceStart,
			); position >= 0 {
				overlapStart = position
			} else if i > 0 && chunks[i-1].Mapped {
				overlapStart = max(
					chunks[i-1].SourceStart,
					chunks[i-1].SourceEnd-chunks[i].ActualOverlap,
				)
			}
		}

		chunks[i].Mapped = true
		chunks[i].SourceStart = sourceStart
		chunks[i].SourceEnd = sourceEnd
		chunks[i].OverlapStart = overlapStart
		cursor = sourceEnd
		mappedChunks++
	}
	return mappedChunks
}

func findRuneRange(source []rune, target []rune, start int) (int, int) {
	if len(target) == 0 {
		return start, start
	}
	for i := max(start, 0); i+len(target) <= len(source); i++ {
		if equalRunes(source[i:i+len(target)], target) {
			return i, i + len(target)
		}
	}
	for i := max(start, 0); i < len(source); i++ {
		if end, ok := matchFlexibleWhitespace(source, target, i); ok {
			return i, end
		}
	}
	return -1, -1
}

func matchFlexibleWhitespace(
	source []rune,
	target []rune,
	sourceStart int,
) (int, bool) {
	sourceIndex := sourceStart
	targetIndex := 0
	for targetIndex < len(target) {
		if sourceIndex >= len(source) {
			return 0, false
		}
		if unicode.IsSpace(target[targetIndex]) {
			if !unicode.IsSpace(source[sourceIndex]) {
				return 0, false
			}
			for targetIndex < len(target) &&
				unicode.IsSpace(target[targetIndex]) {
				targetIndex++
			}
			for sourceIndex < len(source) &&
				unicode.IsSpace(source[sourceIndex]) {
				sourceIndex++
			}
			continue
		}
		if source[sourceIndex] != target[targetIndex] {
			return 0, false
		}
		sourceIndex++
		targetIndex++
	}
	return sourceIndex, true
}

func findLastRunes(
	source []rune,
	target []rune,
	start int,
	end int,
) int {
	if len(target) == 0 {
		return end
	}
	for i := min(end-len(target), len(source)-len(target)); i >= start; i-- {
		if equalRunes(source[i:i+len(target)], target) {
			return i
		}
	}
	return -1
}

func equalRunes(left []rune, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func writeError(w http.ResponseWriter, message string) {
	writeErrorStatus(w, http.StatusBadRequest, message)
}

func writeErrorStatus(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
