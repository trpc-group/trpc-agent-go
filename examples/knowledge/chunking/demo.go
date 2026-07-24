//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package chunkingdemo shares the sample and strategy execution used by the
// text and web chunking examples.
package chunkingdemo

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	frameworkchunking "trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	documentreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/csv"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/json"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
)

const (
	defaultChunkSize     = 1024
	defaultJSONChunkSize = 2000
)

//go:embed samples/sample.md
var sampleMarkdown string

//go:embed samples/sample-edge.md
var sampleEdgeMarkdown string

//go:embed samples/sample-issue-2200.md
var sampleIssue2200Markdown string

//go:embed samples/sample-catalog.md
var sampleCatalogMarkdown string

//go:embed samples/sample.txt
var sampleText string

//go:embed samples/sample.csv
var sampleCSV string

//go:embed samples/sample.json
var sampleJSON string

// SampleDocument describes one document bundled with the example.
type SampleDocument struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

var sampleDocuments = []SampleDocument{
	{
		ID:          "markdown-overview",
		Name:        "sample.md",
		Label:       "Markdown overview",
		Description: "Headings, a table, mixed Chinese and English, a fenced code block, emoji, and a long token.",
		Content:     sampleMarkdown,
	},
	{
		ID:          "markdown-edge-cases",
		Name:        "sample-edge.md",
		Label:       "Markdown edge cases",
		Description: "Sparse headings, nested blocks, a wide table, a code block larger than a small chunk budget, and Unicode edge cases.",
		Content:     sampleEdgeMarkdown,
	},
	{
		ID:          "issue-2200-regression",
		Name:        "sample-issue-2200.md",
		Label:       "Issue #2200 regression",
		Description: "Strict overlap budgets, English and CJK boundaries, numeric dots, version labels, punctuation clusters, and rune fallback.",
		Content:     sampleIssue2200Markdown,
	},
	{
		ID:          "markdown-value-catalog",
		Name:        "sample-catalog.md",
		Label:       "Markdown value catalog",
		Description: "A realistic reference-document shape with grouped rows, several tables, short sections, and one long table cell.",
		Content:     sampleCatalogMarkdown,
	},
	{
		ID:          "plain-text",
		Name:        "sample.txt",
		Label:       "Plain text",
		Description: "Paragraph, sentence, whitespace, CJK punctuation, emoji, and unbroken-token boundaries for TextReader.",
		Content:     sampleText,
	},
	{
		ID:          "csv-records",
		Name:        "sample.csv",
		Label:       "CSV records",
		Description: "Multilingual rows, empty fields, emoji, URLs, and long values normalized by CSVReader before chunking.",
		Content:     sampleCSV,
	},
	{
		ID:          "nested-json",
		Name:        "sample.json",
		Label:       "Nested JSON",
		Description: "Nested objects, arrays, multilingual strings, empty values, and large properties for JSONChunking.",
		Content:     sampleJSON,
	},
}

// Result contains the chunks produced by one strategy.
type Result struct {
	Name      string
	Reader    string
	Automatic bool
	ChunkSize int
	Overlap   int
	SizeUnit  string
	Chunks    []*document.Document
}

// Sample returns the bundled Markdown document.
func Sample() string {
	return sampleMarkdown
}

// Samples returns the documents bundled with the interactive example.
func Samples() []SampleDocument {
	result := make([]SampleDocument, len(sampleDocuments))
	copy(result, sampleDocuments)
	return result
}

// Run applies the selected chunking strategies to a document.
func Run(
	strategyName string,
	documentName string,
	content string,
	chunkSize int,
	overlap int,
) ([]Result, error) {
	if strategyName == "" {
		strategyName = "reader"
	}
	if chunkSize < 0 {
		return nil, fmt.Errorf("chunk size must not be negative")
	}
	if documentName == "" {
		documentName = "document.txt"
	}
	if strategyName == "reader" {
		return runReaderDefault(
			documentName,
			content,
			chunkSize,
			overlap,
		)
	}
	effectiveChunkSize := resolveChunkSize(strategyName, chunkSize)
	if overlap < 0 || overlap >= effectiveChunkSize {
		return nil, fmt.Errorf("overlap must be between 0 and chunk size - 1")
	}
	if strategyName == "json" && overlap != 0 {
		return nil, fmt.Errorf("json chunking does not support overlap")
	}

	fixedOptions := []frameworkchunking.Option{
		frameworkchunking.WithChunkSize(effectiveChunkSize),
		frameworkchunking.WithOverlap(overlap),
	}
	if strings.EqualFold(filepath.Ext(documentName), ".csv") {
		fixedOptions = append(
			fixedOptions,
			frameworkchunking.WithPreserveLines(),
		)
	}
	strategies := []struct {
		name     string
		strategy frameworkchunking.Strategy
	}{
		{
			name:     "fixed",
			strategy: frameworkchunking.NewFixedSizeChunking(fixedOptions...),
		},
		{
			name: "recursive",
			strategy: frameworkchunking.NewRecursiveChunking(
				frameworkchunking.WithRecursiveChunkSize(effectiveChunkSize),
				frameworkchunking.WithRecursiveOverlap(overlap),
			),
		},
		{
			name: "markdown",
			strategy: frameworkchunking.NewMarkdownChunking(
				frameworkchunking.WithMarkdownChunkSize(effectiveChunkSize),
				frameworkchunking.WithMarkdownOverlap(overlap),
			),
		},
		{
			name: "json",
			strategy: frameworkchunking.NewJSONChunking(
				frameworkchunking.WithJSONChunkSize(effectiveChunkSize),
			),
		},
	}

	if strategyName == "all" {
		// JSONChunking requires JSON input, so the default text comparison
		// includes only strategies that accept the bundled Markdown sample.
		strategies = strategies[:3]
	} else {
		selected := strategies[:0]
		for _, candidate := range strategies {
			if candidate.name == strategyName {
				selected = append(selected, candidate)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf(
				"unknown strategy %q; use reader, all, fixed, recursive, markdown, or json",
				strategyName,
			)
		}
		strategies = selected
	}

	doc := &document.Document{
		ID:      "chunking-demo",
		Name:    documentName,
		Content: content,
	}
	results := make([]Result, 0, len(strategies))
	for _, candidate := range strategies {
		chunks, err := candidate.strategy.Chunk(doc)
		if err != nil {
			return nil, fmt.Errorf("%s chunking: %w", candidate.name, err)
		}
		results = append(results, Result{
			Name:      candidate.name,
			ChunkSize: effectiveChunkSize,
			Overlap:   overlap,
			SizeUnit:  sizeUnit(candidate.name),
			Chunks:    chunks,
		})
	}
	return results, nil
}

func runReaderDefault(
	documentName string,
	content string,
	configuredChunkSize int,
	overlap int,
) ([]Result, error) {
	extension := strings.ToLower(filepath.Ext(documentName))
	switch extension {
	case ".md", ".markdown", ".json", ".csv", ".txt", ".text":
	default:
		extension = ".txt"
	}

	var opts []documentreader.Option
	if configuredChunkSize > 0 {
		opts = append(opts, documentreader.WithChunkSize(configuredChunkSize))
	}
	if overlap > 0 {
		opts = append(opts, documentreader.WithChunkOverlap(overlap))
	}
	selectedReader, ok := documentreader.GetReader(extension, opts...)
	if !ok {
		return nil, fmt.Errorf("no reader registered for %s", extension)
	}

	strategyName := readerStrategyName(selectedReader.Name())
	effectiveChunkSize := resolveChunkSize(strategyName, configuredChunkSize)
	if overlap < 0 || overlap >= effectiveChunkSize {
		return nil, fmt.Errorf("overlap must be between 0 and chunk size - 1")
	}
	if strategyName == "json" && overlap != 0 {
		return nil, fmt.Errorf(
			"%s selects JSONChunking, which does not support overlap",
			selectedReader.Name(),
		)
	}
	chunks, err := selectedReader.ReadFromReader(
		documentName,
		strings.NewReader(content),
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", selectedReader.Name(), err)
	}
	return []Result{{
		Name:      strategyName,
		Reader:    selectedReader.Name(),
		Automatic: true,
		ChunkSize: effectiveChunkSize,
		Overlap:   overlap,
		SizeUnit:  sizeUnit(strategyName),
		Chunks:    chunks,
	}}, nil
}

func resolveChunkSize(strategyName string, configuredSize int) int {
	if configuredSize > 0 {
		return configuredSize
	}
	if strategyName == "json" {
		return defaultJSONChunkSize
	}
	return defaultChunkSize
}

func readerStrategyName(readerName string) string {
	switch readerName {
	case "MarkdownReader":
		return "markdown"
	case "JSONReader":
		return "json"
	default:
		return "fixed"
	}
}

func sizeUnit(strategyName string) string {
	if strategyName == "json" {
		return "bytes"
	}
	return "runes"
}

// BoundaryOverlap returns the rune length shared by the end of previous and
// the beginning of current, up to limit.
func BoundaryOverlap(previous string, current string, limit int) int {
	previousRunes := []rune(previous)
	currentRunes := []rune(current)
	maxSize := min(limit, len(previousRunes), len(currentRunes))
	for size := maxSize; size > 0; size-- {
		if string(previousRunes[len(previousRunes)-size:]) == string(currentRunes[:size]) {
			return size
		}
	}
	return 0
}
