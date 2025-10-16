//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"encoding/json"
	"fmt"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// JSONChunking implements a chunking strategy optimized for JSON documents.
type JSONChunking struct {
	maxChunkSize int
	minChunkSize int
}

// JSONOption represents a functional option for configuring JSONChunking.
type JSONOption func(*JSONChunking)

// WithJSONChunkSize sets the maximum size of each chunk in characters.
func WithJSONChunkSize(size int) JSONOption {
	const minChunkSize = 50
	const margin = 200
	return func(j *JSONChunking) {
		j.maxChunkSize = size
		j.minChunkSize = max(size-margin, minChunkSize)
	}
}

// WithJSONMinChunkSize sets the minimum size of each chunk in characters.
func WithJSONMinChunkSize(size int) JSONOption {
	return func(j *JSONChunking) {
		j.minChunkSize = size
	}
}

// NewJSONChunking creates a new JSON chunking strategy with the given options.
func NewJSONChunking(opts ...JSONOption) *JSONChunking {
	j := &JSONChunking{
		maxChunkSize: 2000,
		minChunkSize: 1800,
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Chunk splits a JSON document into smaller chunks while preserving structure.
func (j *JSONChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	// Parse JSON content.
	var jsonData any
	if err := json.Unmarshal([]byte(doc.Content), &jsonData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Convert to map for processing.
	dataMap, ok := jsonData.(map[string]any)
	if !ok {
		// If not a map, wrap it in a map for processing.
		dataMap = map[string]any{"content": jsonData}
	}

	// Split JSON into chunks.
	chunks := j.splitJSON(dataMap, false)

	// Convert chunks to documents.
	var documents []*document.Document
	for i, chunk := range chunks {
		chunkJSON, err := json.Marshal(chunk)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chunk %d: %w", i, err)
		}

		chunkDoc := createChunk(doc, string(chunkJSON), i+1)
		chunkDoc.Metadata[source.MetaChunkType] = "json"
		documents = append(documents, chunkDoc)
	}

	return documents, nil
}

// splitJSON recursively splits JSON data into chunks while preserving hierarchy.
func (j *JSONChunking) splitJSON(data map[string]any, convertLists bool) []map[string]any {
	// Preprocess data if convertLists is true.
	if convertLists {
		processed := j.listToDictPreprocessing(data)
		if processedMap, ok := processed.(map[string]any); ok {
			data = processedMap
		}
	}

	// Split the JSON data.
	chunks := j.jsonSplit(data, nil, []map[string]any{{}})

	// Remove empty chunks.
	if len(chunks) > 0 && len(chunks[len(chunks)-1]) == 0 {
		chunks = chunks[:len(chunks)-1]
	}

	return chunks
}

// jsonSplit recursively splits JSON into maximum size dictionaries while preserving structure.
func (j *JSONChunking) jsonSplit(
	data map[string]any,
	currentPath []string,
	chunks []map[string]any,
) []map[string]any {
	if currentPath == nil {
		currentPath = []string{}
	}

	for key, value := range data {
		newPath := append(currentPath, key)
		chunkSize := j.jsonSize(chunks[len(chunks)-1])
		size := j.jsonSize(map[string]any{key: value})
		remaining := j.maxChunkSize - chunkSize

		if size < remaining {
			// Add item to current chunk.
			j.setNestedDict(chunks[len(chunks)-1], newPath, value)
		} else {
			if chunkSize >= j.minChunkSize {
				// Chunk is big enough, start a new chunk.
				chunks = append(chunks, map[string]any{})
			}

			// Recursively process nested structures.
			if nestedMap, ok := value.(map[string]any); ok {
				chunks = j.jsonSplit(nestedMap, newPath, chunks)
			} else if nestedSlice, ok := value.([]any); ok {
				// Handle arrays by converting to map if needed.
				chunks = j.jsonSplit(j.arrayToMap(nestedSlice), newPath, chunks)
			} else {
				// Handle single item.
				j.setNestedDict(chunks[len(chunks)-1], newPath, value)
			}
		}
	}

	return chunks
}

// jsonSize calculates the size of the serialized JSON object.
func (j *JSONChunking) jsonSize(data map[string]any) int {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return 0
	}
	return len(jsonBytes)
}

// setNestedDict sets a value in a nested dictionary based on the given path.
func (j *JSONChunking) setNestedDict(d map[string]any, path []string, value any) {
	current := d
	for _, key := range path[:len(path)-1] {
		if nested, exists := current[key]; exists {
			if nestedMap, ok := nested.(map[string]any); ok {
				current = nestedMap
			} else {
				// Create new map if key exists but is not a map.
				newMap := map[string]any{}
				current[key] = newMap
				current = newMap
			}
		} else {
			newMap := map[string]any{}
			current[key] = newMap
			current = newMap
		}
	}
	current[path[len(path)-1]] = value
}

// listToDictPreprocessing converts lists to dictionaries for better chunking.
func (j *JSONChunking) listToDictPreprocessing(data any) any {
	switch v := data.(type) {
	case map[string]any:
		// Process each key-value pair in the dictionary.
		result := make(map[string]any)
		for k, val := range v {
			result[k] = j.listToDictPreprocessing(val)
		}
		return result
	case []any:
		// Convert the list to a dictionary with index-based keys.
		result := make(map[string]any)
		for i, item := range v {
			result[strconv.Itoa(i)] = j.listToDictPreprocessing(item)
		}
		return result
	default:
		// Base case: the item is neither a dict nor a list, so return it unchanged.
		return data
	}
}

// arrayToMap converts an array to a map with index-based keys.
func (j *JSONChunking) arrayToMap(arr []any) map[string]any {
	result := make(map[string]any)
	for i, item := range arr {
		result[strconv.Itoa(i)] = item
	}
	return result
}

// SplitJSON splits JSON data into chunks and returns them as strings.
func (j *JSONChunking) SplitJSON(data map[string]any, convertLists bool) ([]string, error) {
	chunks := j.splitJSON(data, convertLists)

	var result []string
	for _, chunk := range chunks {
		jsonBytes, err := json.Marshal(chunk)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chunk: %w", err)
		}
		result = append(result, string(jsonBytes))
	}

	return result, nil
}

// SplitJSONString splits a JSON string into chunks.
func (j *JSONChunking) SplitJSONString(jsonStr string, convertLists bool) ([]string, error) {
	var data map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON string: %w", err)
	}

	return j.SplitJSON(data, convertLists)
}

// Name returns the name of this chunking strategy.
func (j *JSONChunking) Name() string {
	return "JSONChunking"
}

// String returns a string representation of the JSON chunking strategy.
func (j *JSONChunking) String() string {
	return fmt.Sprintf("JSONChunking(maxChunkSize=%d, minChunkSize=%d)",
		j.maxChunkSize, j.minChunkSize)
}
