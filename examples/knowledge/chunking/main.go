//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main compares the chunk boundaries produced by the built-in
// knowledge chunking strategies.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

//go:embed sample.md
var sampleMarkdown string

func main() {
	strategyName := flag.String(
		"strategy",
		"all",
		"chunking strategy to run: all, fixed, recursive, or markdown",
	)
	inputPath := flag.String(
		"input",
		"",
		"path to a Markdown file; uses the embedded sample.md when empty",
	)
	chunkSize := flag.Int("chunk-size", 240, "maximum chunk size in Unicode runes")
	overlap := flag.Int("overlap", 0, "overlap between adjacent chunks in Unicode runes")
	maxChunks := flag.Int("max-chunks", 20, "maximum chunks to print per strategy; 0 prints all")
	flag.Parse()

	if *chunkSize <= 0 {
		log.Fatalf("chunk-size must be greater than 0")
	}
	if *overlap < 0 || *overlap >= *chunkSize {
		log.Fatalf("overlap must be between 0 and chunk-size - 1")
	}
	if *maxChunks < 0 {
		log.Fatalf("max-chunks must be non-negative")
	}

	content := sampleMarkdown
	inputName := "embedded sample.md"
	if *inputPath != "" {
		data, err := os.ReadFile(*inputPath)
		if err != nil {
			log.Fatalf("read input: %v", err)
		}
		content = string(data)
		inputName = filepath.Clean(*inputPath)
	}

	doc := &document.Document{
		ID:      "chunking-demo",
		Name:    filepath.Base(inputName),
		Content: content,
	}

	strategies := []struct {
		name     string
		strategy chunking.Strategy
	}{
		{
			name: "fixed",
			strategy: chunking.NewFixedSizeChunking(
				chunking.WithChunkSize(*chunkSize),
				chunking.WithOverlap(*overlap),
			),
		},
		{
			name: "recursive",
			strategy: chunking.NewRecursiveChunking(
				chunking.WithRecursiveChunkSize(*chunkSize),
				chunking.WithRecursiveOverlap(*overlap),
			),
		},
		{
			name: "markdown",
			strategy: chunking.NewMarkdownChunking(
				chunking.WithMarkdownChunkSize(*chunkSize),
				chunking.WithMarkdownOverlap(*overlap),
			),
		},
	}

	if *strategyName != "all" {
		selected := strategies[:0]
		for _, candidate := range strategies {
			if candidate.name == *strategyName {
				selected = append(selected, candidate)
			}
		}
		if len(selected) == 0 {
			log.Fatalf("unknown strategy %q; use all, fixed, recursive, or markdown", *strategyName)
		}
		strategies = selected
	}

	fmt.Println("Knowledge Chunking Demo")
	fmt.Println("=======================")
	fmt.Printf("Input: %s\n", inputName)
	fmt.Printf(
		"Input size: %d runes, %d bytes\n",
		utf8.RuneCountInString(content),
		len(content),
	)
	fmt.Printf("Chunk size: %d runes\n", *chunkSize)
	fmt.Printf("Overlap: %d runes\n", *overlap)

	for _, candidate := range strategies {
		chunks, err := candidate.strategy.Chunk(doc)
		if err != nil {
			log.Fatalf("%s chunking: %v", candidate.name, err)
		}
		printChunks(candidate.name, chunks, *chunkSize, *overlap, *maxChunks)
	}
}

func printChunks(
	strategyName string,
	chunks []*document.Document,
	chunkSize int,
	overlapLimit int,
	maxChunks int,
) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 72))
	fmt.Printf("%s: %d chunks\n", strings.ToUpper(strategyName), len(chunks))
	fmt.Println(strings.Repeat("=", 72))

	printCount := len(chunks)
	if maxChunks > 0 {
		printCount = min(printCount, maxChunks)
	}
	for i, chunk := range chunks[:printCount] {
		runeSize := utf8.RuneCountInString(chunk.Content)
		actualOverlap := 0
		if i > 0 {
			actualOverlap = sharedBoundarySize(
				chunks[i-1].Content,
				chunk.Content,
				overlapLimit,
			)
		}

		fmt.Printf(
			"\n--- chunk %d | id=%s | runes=%d | bytes=%d | within_budget=%t",
			i+1,
			chunk.ID,
			runeSize,
			len(chunk.Content),
			runeSize <= chunkSize,
		)
		if i > 0 {
			fmt.Printf(" | overlap_with_previous=%d", actualOverlap)
		}
		if metadataSize, ok := chunk.Metadata[source.MetaChunkSize]; ok {
			fmt.Printf(" | metadata_size=%v", metadataSize)
		}
		if overlappedSize, ok := chunk.Metadata[source.MetaOverlappedContentSize]; ok {
			fmt.Printf(" | overlapped_metadata_size=%v", overlappedSize)
		}
		if headerPath, ok := chunk.Metadata[source.MetaMarkdownHeaderPath]; ok {
			fmt.Printf(" | header_path=%q", headerPath)
		}
		fmt.Println(" ---")
		fmt.Println(chunk.Content)
		fmt.Printf("--- end chunk %d ---\n", i+1)
	}
	if printCount < len(chunks) {
		fmt.Printf(
			"\n... %d chunks omitted; use -max-chunks 0 to print all ...\n",
			len(chunks)-printCount,
		)
	}
}

func sharedBoundarySize(previous string, current string, limit int) int {
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
