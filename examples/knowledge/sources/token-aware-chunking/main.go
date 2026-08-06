//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates token-aware Markdown chunking through FileSource.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	tiktoken "trpc.group/trpc-go/trpc-agent-go/model/tiktoken"
)

const (
	modelName    = "text-embedding-3-small"
	chunkSize    = 48
	chunkOverlap = 8
)

func main() {
	counter, err := tiktoken.New(modelName)
	if err != nil {
		log.Fatalf("Create tokenizer: %v", err)
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("Locate example directory")
	}
	filePath := filepath.Join(filepath.Dir(currentFile), "sample.md")

	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Read sample: %v", err)
	}
	sourceTokens, err := counter.CountText(string(content))
	if err != nil {
		log.Fatalf("Count source tokens: %v", err)
	}

	fileSource := filesource.New(
		[]string{filePath},
		filesource.WithChunkSize(chunkSize),
		filesource.WithChunkOverlap(chunkOverlap),
		filesource.WithChunkLengthFunc(counter.CountText),
	)
	chunks, err := fileSource.ReadDocuments(context.Background())
	if err != nil {
		log.Fatalf("Chunk sample: %v", err)
	}

	fmt.Printf("Tokenizer: %s\n", modelName)
	fmt.Printf(
		"Source: %d tokens, %d runes\n",
		sourceTokens,
		utf8.RuneCount(content),
	)
	fmt.Printf(
		"Budget: %d tokens, overlap: at most %d tokens\n",
		chunkSize,
		chunkOverlap,
	)
	fmt.Printf("Chunks: %d\n", len(chunks))

	for i, chunk := range chunks {
		tokens, err := counter.CountText(chunk.Content)
		if err != nil {
			log.Fatalf("Count chunk %d tokens: %v", i+1, err)
		}
		if tokens > chunkSize {
			log.Fatalf(
				"Chunk %d uses %d tokens, exceeding budget %d",
				i+1,
				tokens,
				chunkSize,
			)
		}
		headerPath, _ :=
			chunk.Metadata[source.MetaMarkdownHeaderPath].(string)
		fmt.Printf(
			"\n--- Chunk %d: %d tokens, %d runes, header=%q ---\n%s\n",
			i+1,
			tokens,
			utf8.RuneCountInString(chunk.Content),
			headerPath,
			chunk.Content,
		)
	}
}
