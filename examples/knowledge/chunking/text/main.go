//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main prints a compact comparison of knowledge chunking strategies.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	chunkingdemo "trpc.group/trpc-go/trpc-agent-go/examples/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func main() {
	strategyName := flag.String(
		"strategy",
		"reader",
		"chunking mode: reader (recommended), all, fixed, recursive, markdown, or json",
	)
	inputPath := flag.String(
		"input",
		"",
		"path to an input file; uses the bundled sample.md when empty",
	)
	chunkSize := flag.Int(
		"chunk-size",
		0,
		"maximum chunk size; 0 uses the reader/strategy default",
	)
	overlap := flag.Int("overlap", 0, "overlap between adjacent chunks in Unicode runes")
	maxChunks := flag.Int(
		"max-chunks",
		8,
		"maximum chunks shown per strategy; 0 shows all",
	)
	flag.Parse()

	if *maxChunks < 0 {
		log.Fatalf("max-chunks must be non-negative")
	}

	content := chunkingdemo.Sample()
	inputName := "bundled sample.md"
	documentName := "sample.md"
	if *inputPath != "" {
		data, err := os.ReadFile(*inputPath)
		if err != nil {
			log.Fatalf("read input: %v", err)
		}
		content = string(data)
		inputName = filepath.Clean(*inputPath)
		documentName = filepath.Base(*inputPath)
	}

	results, err := chunkingdemo.Run(
		*strategyName,
		documentName,
		content,
		*chunkSize,
		*overlap,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Knowledge Chunking Comparison")
	fmt.Printf("input=%s runes=%d bytes=%d requested_chunk_size=%d requested_overlap=%d\n",
		inputName,
		utf8.RuneCountInString(content),
		len(content),
		*chunkSize,
		*overlap,
	)
	for _, result := range results {
		printResult(result, result.ChunkSize, result.Overlap, *maxChunks)
	}
}

func printResult(
	result chunkingdemo.Result,
	chunkSize int,
	overlapLimit int,
	maxChunks int,
) {
	sizeUnit := result.SizeUnit
	minSize, maxSize, average, overBudget := stats(
		result.Chunks,
		chunkSize,
		sizeUnit,
	)
	selection := strings.ToUpper(result.Name)
	if result.Automatic {
		selection = fmt.Sprintf("%s -> %s", result.Reader, selection)
	}
	fmt.Printf(
		"\n%s chunks=%d chunk_size=%d overlap=%d min=%d max=%d avg=%.1f size_unit=%s over_budget=%d\n",
		selection,
		len(result.Chunks),
		result.ChunkSize,
		result.Overlap,
		minSize,
		maxSize,
		average,
		sizeUnit,
		overBudget,
	)

	indexes := displayedIndexes(len(result.Chunks), maxChunks)
	previousIndex := -1
	for _, index := range indexes {
		if previousIndex >= 0 && index > previousIndex+1 {
			fmt.Printf("  ... %d chunks omitted ...\n", index-previousIndex-1)
		}
		chunk := result.Chunks[index]
		runeSize := utf8.RuneCountInString(chunk.Content)
		configuredSize := runeSize
		if sizeUnit == "bytes" {
			configuredSize = len(chunk.Content)
		}
		actualOverlap := 0
		if index > 0 {
			actualOverlap = chunkingdemo.BoundaryOverlap(
				result.Chunks[index-1].Content,
				chunk.Content,
				overlapLimit,
			)
		}
		budget := "ok"
		if configuredSize > chunkSize {
			budget = fmt.Sprintf("over+%d", configuredSize-chunkSize)
		}

		fmt.Printf(
			"  #%02d %-18s %4dr/%4db %-8s ov=%-3d %-42s | %q\n",
			index+1,
			chunk.ID,
			runeSize,
			len(chunk.Content),
			budget,
			actualOverlap,
			metadata(chunk),
			contentPreview(chunk.Content),
		)
		previousIndex = index
	}
}

func stats(
	chunks []*document.Document,
	chunkSize int,
	sizeUnit string,
) (int, int, float64, int) {
	if len(chunks) == 0 {
		return 0, 0, 0, 0
	}
	minSize := configuredSize(chunks[0].Content, sizeUnit)
	maxSize := minSize
	totalSize := 0
	overBudget := 0
	for _, chunk := range chunks {
		size := configuredSize(chunk.Content, sizeUnit)
		minSize = min(minSize, size)
		maxSize = max(maxSize, size)
		totalSize += size
		if size > chunkSize {
			overBudget++
		}
	}
	return minSize, maxSize, float64(totalSize) / float64(len(chunks)), overBudget
}

func configuredSize(content string, sizeUnit string) int {
	if sizeUnit == "bytes" {
		return len(content)
	}
	return utf8.RuneCountInString(content)
}

func displayedIndexes(total int, limit int) []int {
	if limit == 0 || total <= limit {
		indexes := make([]int, total)
		for i := range indexes {
			indexes[i] = i
		}
		return indexes
	}

	headCount := limit / 2
	tailCount := limit - headCount
	indexes := make([]int, 0, limit)
	for i := 0; i < headCount; i++ {
		indexes = append(indexes, i)
	}
	for i := total - tailCount; i < total; i++ {
		indexes = append(indexes, i)
	}
	return indexes
}

func metadata(chunk *document.Document) string {
	var values []string
	if size, ok := chunk.Metadata[source.MetaChunkSize]; ok {
		values = append(values, fmt.Sprintf("size=%v", size))
	}
	if size, ok := chunk.Metadata[source.MetaOverlappedContentSize]; ok {
		values = append(values, fmt.Sprintf("overlapped=%v", size))
	}
	if path, ok := chunk.Metadata[source.MetaMarkdownHeaderPath]; ok {
		values = append(values, fmt.Sprintf("header=%v", path))
	}
	if len(values) == 0 {
		return "meta{}"
	}
	return "meta{" + strings.Join(values, " ") + "}"
}

func contentPreview(content string) string {
	const (
		headRunes = 48
		tailRunes = 24
	)
	normalized := strings.ReplaceAll(content, "\n", " ↵ ")
	runes := []rune(normalized)
	if len(runes) <= headRunes+tailRunes {
		return normalized
	}
	return string(runes[:headRunes]) + " … " + string(runes[len(runes)-tailRunes:])
}
