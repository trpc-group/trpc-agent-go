//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to configure chunking through a file source.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

const (
	chunkSize       = 240
	chunkOverlap    = 24
	displayedChunks = 3
)

func main() {
	inputPath := flag.String(
		"input",
		"./chunking/samples/sample.md",
		"path to the document to chunk",
	)
	flag.Parse()
	ctx := context.Background()
	fmt.Printf("Input: %s\n", *inputPath)

	// Recommended: configure chunking on the source. FileSource selects a Reader
	// from the file extension and the Reader selects its format-aware strategy.
	src := file.New(
		[]string{*inputPath},
		file.WithChunkSize(chunkSize),
		file.WithChunkOverlap(chunkOverlap),
	)
	chunks, err := src.ReadDocuments(ctx)
	if err != nil {
		log.Fatal(err)
	}
	printChunks(
		"Source default",
		"FileSource -> format-aware Reader/strategy",
		chunks,
	)

	// Advanced: override the Reader's strategy through the same source API.
	strategy := chunking.NewRecursiveChunking(
		chunking.WithRecursiveChunkSize(chunkSize),
		chunking.WithRecursiveOverlap(chunkOverlap),
	)
	src = file.New(
		[]string{*inputPath},
		file.WithCustomChunkingStrategy(strategy),
	)
	chunks, err = src.ReadDocuments(ctx)
	if err != nil {
		log.Fatal(err)
	}
	printChunks(
		"Custom strategy",
		"FileSource -> RecursiveChunking override",
		chunks,
	)
}

func printChunks(title string, selection string, chunks []*document.Document) {
	fmt.Printf("\n%s\n", title)
	fmt.Printf(
		"  %s | chunk size: %d runes | overlap: %d runes | chunks: %d\n",
		selection,
		chunkSize,
		chunkOverlap,
		len(chunks),
	)

	limit := min(len(chunks), displayedChunks)
	for i := 0; i < limit; i++ {
		chunk := chunks[i]
		fmt.Printf(
			"\n  Chunk %d/%d | %d runes | %s\n",
			i+1,
			len(chunks),
			utf8.RuneCountInString(chunk.Content),
			chunkMetadata(chunk),
		)
		fmt.Printf("  %s\n", contentPreview(chunk.Content))
	}
	if len(chunks) > limit {
		fmt.Printf("\n  ... %d more chunks\n", len(chunks)-limit)
	}
}

func chunkMetadata(chunk *document.Document) string {
	values := []string{
		fmt.Sprintf("index=%v", chunk.Metadata[source.MetaChunkIndex]),
		fmt.Sprintf("chunk_size=%v", chunk.Metadata[source.MetaChunkSize]),
	}
	if overlap, ok := chunk.Metadata[source.MetaOverlappedContentSize]; ok {
		values = append(values, fmt.Sprintf("overlapped_content_size=%v", overlap))
	}
	if headerPath, ok := chunk.Metadata[source.MetaMarkdownHeaderPath]; ok {
		values = append(values, fmt.Sprintf("markdown_header_path=%v", headerPath))
	}
	return "metadata{" + strings.Join(values, ", ") + "}"
}

func contentPreview(content string) string {
	const maxRunes = 180

	normalized := strings.Join(strings.Fields(content), " ")
	runes := []rune(normalized)
	if len(runes) <= maxRunes {
		return normalized
	}
	return string(runes[:maxRunes]) + "..."
}
