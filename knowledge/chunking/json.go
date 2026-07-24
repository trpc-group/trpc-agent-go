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
	"sort"
	"strconv"
	"strings"

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

// WithJSONChunkSize sets the maximum serialized size of each chunk in bytes.
func WithJSONChunkSize(size int) JSONOption {
	const minChunkSize = 50
	const margin = 200
	return func(j *JSONChunking) {
		j.maxChunkSize = size
		j.minChunkSize = max(size-margin, minChunkSize)
	}
}

// WithJSONMinChunkSize sets the minimum serialized size of each chunk in bytes.
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
	chunks, err := j.splitJSON(dataMap, false)
	if err != nil {
		return nil, err
	}

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
func (j *JSONChunking) splitJSON(
	data map[string]any,
	convertLists bool,
) ([]map[string]any, error) {
	// Preprocess data if convertLists is true.
	if convertLists {
		processed := j.listToDictPreprocessing(data)
		if processedMap, ok := processed.(map[string]any); ok {
			data = processedMap
		}
	}

	// Split the JSON data.
	chunks, err := j.jsonSplit(data, nil, []map[string]any{{}})
	if err != nil {
		return nil, err
	}

	// Remove empty chunks.
	if len(chunks) > 0 && len(chunks[len(chunks)-1]) == 0 {
		chunks = chunks[:len(chunks)-1]
	}

	return chunks, nil
}

// jsonSplit recursively splits JSON into maximum size dictionaries while preserving structure.
func (j *JSONChunking) jsonSplit(
	data map[string]any,
	currentPath []string,
	chunks []map[string]any,
) ([]map[string]any, error) {
	if currentPath == nil {
		currentPath = []string{}
	}

	for _, key := range orderedJSONKeys(data) {
		value := data[key]
		newPath := append(append([]string{}, currentPath...), key)
		if candidate, ok := j.withValueIfFits(
			chunks[len(chunks)-1],
			newPath,
			value,
		); ok {
			chunks[len(chunks)-1] = candidate
			continue
		}
		if len(chunks[len(chunks)-1]) > 0 &&
			j.jsonSize(chunks[len(chunks)-1]) >= j.minChunkSize {
			chunks = append(chunks, map[string]any{})
			if candidate, ok := j.withValueIfFits(
				chunks[len(chunks)-1],
				newPath,
				value,
			); ok {
				chunks[len(chunks)-1] = candidate
				continue
			}
		}

		switch nested := value.(type) {
		case map[string]any:
			if len(nested) == 0 {
				var err error
				chunks, err = j.addAtomicValue(chunks, newPath, value)
				if err != nil {
					return nil, err
				}
				continue
			}
			var err error
			chunks, err = j.jsonSplit(nested, newPath, chunks)
			if err != nil {
				return nil, err
			}
		case []any:
			if len(nested) == 0 {
				var err error
				chunks, err = j.addAtomicValue(chunks, newPath, value)
				if err != nil {
					return nil, err
				}
				continue
			}
			var err error
			chunks, err = j.jsonSplit(
				j.arrayToMap(nested),
				newPath,
				chunks,
			)
			if err != nil {
				return nil, err
			}
		case string:
			var err error
			if nested == "" {
				chunks, err = j.addAtomicValue(chunks, newPath, nested)
			} else {
				chunks, err = j.addStringValue(chunks, newPath, nested)
			}
			if err != nil {
				return nil, err
			}
		default:
			var err error
			chunks, err = j.addAtomicValue(chunks, newPath, value)
			if err != nil {
				return nil, err
			}
		}
	}

	return chunks, nil
}

func orderedJSONKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, k int) bool {
		left, leftErr := strconv.Atoi(keys[i])
		right, rightErr := strconv.Atoi(keys[k])
		leftNumeric := leftErr == nil
		rightNumeric := rightErr == nil
		if leftNumeric != rightNumeric {
			return leftNumeric
		}
		if leftNumeric {
			if left != right {
				return left < right
			}
		}
		return keys[i] < keys[k]
	})
	return keys
}

func (j *JSONChunking) addStringValue(
	chunks []map[string]any,
	path []string,
	value string,
) ([]map[string]any, error) {
	remaining := []rune(value)
	for len(remaining) > 0 {
		current := chunks[len(chunks)-1]
		prefixSize := j.largestStringPrefix(current, path, remaining)
		if prefixSize == 0 && len(current) > 0 {
			chunks = append(chunks, map[string]any{})
			continue
		}
		if prefixSize == 0 {
			candidate := cloneJSONMap(current)
			j.setNestedDict(candidate, path, string(remaining[:1]))
			return nil, fmt.Errorf(
				"json value at %q requires at least %d bytes, exceeds chunk size %d",
				strings.Join(path, "."),
				j.jsonSize(candidate),
				j.maxChunkSize,
			)
		}

		candidate, _ := j.withValueIfFits(
			current,
			path,
			string(remaining[:prefixSize]),
		)
		chunks[len(chunks)-1] = candidate
		remaining = remaining[prefixSize:]
		if len(remaining) > 0 {
			chunks = append(chunks, map[string]any{})
		}
	}
	return chunks, nil
}

func (j *JSONChunking) largestStringPrefix(
	current map[string]any,
	path []string,
	value []rune,
) int {
	left := 1
	right := len(value)
	best := 0
	for left <= right {
		middle := left + (right-left)/2
		if _, ok := j.withValueIfFits(
			current,
			path,
			string(value[:middle]),
		); ok {
			best = middle
			left = middle + 1
		} else {
			right = middle - 1
		}
	}
	return best
}

func (j *JSONChunking) addAtomicValue(
	chunks []map[string]any,
	path []string,
	value any,
) ([]map[string]any, error) {
	if len(chunks[len(chunks)-1]) > 0 {
		chunks = append(chunks, map[string]any{})
	}
	candidate, ok := j.withValueIfFits(
		chunks[len(chunks)-1],
		path,
		value,
	)
	if !ok {
		candidate = cloneJSONMap(chunks[len(chunks)-1])
		j.setNestedDict(candidate, path, value)
		return nil, fmt.Errorf(
			"json value at %q requires %d bytes, exceeds chunk size %d",
			strings.Join(path, "."),
			j.jsonSize(candidate),
			j.maxChunkSize,
		)
	}
	chunks[len(chunks)-1] = candidate
	return chunks, nil
}

func (j *JSONChunking) withValueIfFits(
	current map[string]any,
	path []string,
	value any,
) (map[string]any, bool) {
	candidate := cloneJSONMap(current)
	j.setNestedDict(candidate, path, value)
	return candidate, j.jsonSize(candidate) <= j.maxChunkSize
}

func cloneJSONMap(data map[string]any) map[string]any {
	result := make(map[string]any, len(data))
	for key, value := range data {
		result[key] = cloneJSONValue(value)
	}
	return result
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = cloneJSONValue(item)
		}
		return result
	default:
		return value
	}
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
	chunks, err := j.splitJSON(data, convertLists)
	if err != nil {
		return nil, err
	}

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
