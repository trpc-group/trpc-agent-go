//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the Docling extractor for converting linked PDF
// and HTML content to markdown, and shows how to integrate it with knowledge
// sources.
//
// Prerequisites:
//   - A running Docling Serve instance (default: http://localhost:5001)
//     docker run -p 5001:5001 ghcr.io/docling-project/docling-serve
//
// Example usage:
//
//	cd examples/knowledge/features/extractor
//	go run main.go
//	go run main.go -endpoint http://localhost:5001 -output ./output
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor/docling"
	urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

	// Register readers.
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
	pdfreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

var (
	endpoint  = flag.String("endpoint", "http://localhost:5001", "Docling Serve endpoint")
	outputDir = flag.String("output", "./output", "Directory to save extracted markdown files")
)

const demoPDFURL = "https://arxiv.org/pdf/1706.03762"
const demoHTMLURL = "https://www.rfc-editor.org/rfc/rfc9110.html"

var demoURLs = []string{
	demoPDFURL,
	demoHTMLURL,
}

func main() {
	flag.Parse()
	ctx := context.Background()

	fmt.Println("Docling Extractor Demo")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Endpoint:   %s\n", *endpoint)
	fmt.Printf("Output Dir: %s\n", *outputDir)
	fmt.Printf("PDF URL:    %s\n", demoPDFURL)
	fmt.Printf("HTML URL:   %s\n", demoHTMLURL)
	fmt.Println(strings.Repeat("=", 60))

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	// Create the Docling extractor.
	// By default, OCR is enabled and images use placeholder mode.
	ext := docling.New(
		docling.WithEndpoint(*endpoint),
		docling.WithTimeout(10*time.Minute),
	)
	defer ext.Close()

	fmt.Printf("\nSupported formats: %v\n", ext.SupportedFormats())

	// --- Part 0: Baseline with built-in PDF reader (no extractor) ---
	// Show what the built-in PDF text reader produces for the linked arXiv PDF.
	fmt.Println("\n" + strings.Repeat("-", 60))
	fmt.Println("Part 0: Built-in PDF Reader from URL (no extractor, text only)")
	fmt.Println(strings.Repeat("-", 60))

	pdfReader := pdfreader.New()
	fmt.Printf("\n[1] Reading with built-in PDF reader: %s\n", demoPDFURL)

	startTime := time.Now()
	docs, err := pdfReader.ReadFromURL(demoPDFURL)
	elapsed := time.Since(startTime)
	if err != nil {
		log.Printf("  Read failed: %v", err)
	} else {
		// Combine all chunks into one text for saving.
		var fullText strings.Builder
		for _, doc := range docs {
			fullText.WriteString(doc.Content)
			fullText.WriteString("\n")
		}

		txtPath := filepath.Join(*outputDir, "1706.03762_pdfreader.txt")
		if err := os.WriteFile(txtPath, []byte(fullText.String()), 0644); err != nil {
			log.Printf("  Failed to write %s: %v", txtPath, err)
		} else {
			fmt.Printf("  Chunks:   %d\n", len(docs))
			fmt.Printf("  Output:   %s (%d bytes)\n", txtPath, fullText.Len())
			fmt.Printf("  Time:     %v\n", elapsed)
			fmt.Printf("  Preview:\n")
			printPreview(fullText.String(), 500)
		}
	}

	// --- Part 1: Direct extraction ---
	// Extract the linked PDF and HTML page to markdown using the Docling extractor directly.
	fmt.Println("\n" + strings.Repeat("-", 60))
	fmt.Println("Part 1: Direct Extraction from URLs (PDF/HTML -> Markdown)")
	fmt.Println(strings.Repeat("-", 60))

	for i, rawURL := range demoURLs {
		fmt.Printf("\n[%d] Extracting: %s\n", i+1, rawURL)

		data, err := downloadURL(ctx, rawURL)
		if err != nil {
			log.Printf("  Failed to fetch %s: %v", rawURL, err)
			continue
		}
		fmt.Printf("  Input size: %d bytes\n", len(data))

		startTime := time.Now()
		result, err := ext.Extract(ctx, data)
		elapsed := time.Since(startTime)
		if err != nil {
			log.Printf("  Extraction failed (%.1fs): %v", elapsed.Seconds(), err)
			continue
		}

		content, err := io.ReadAll(result.Reader)
		if err != nil {
			log.Printf("  Failed to read result: %v", err)
			continue
		}

		mdName := outputBaseNameFromURL(rawURL) + "_docling.md"
		mdPath := filepath.Join(*outputDir, mdName)
		if err := os.WriteFile(mdPath, content, 0644); err != nil {
			log.Printf("  Failed to write %s: %v", mdPath, err)
			continue
		}

		fmt.Printf("  Format:   %s\n", result.Format)
		fmt.Printf("  Output:   %s (%d bytes)\n", mdPath, len(content))
		fmt.Printf("  Time:     %v\n", elapsed)
		fmt.Printf("  Preview:\n")
		printPreview(string(content), 500)
	}

	// --- Part 2: URL Source with extractor ---
	// Show how url.WithExtractor integrates Docling for linked PDF and HTML content,
	// and write chunk results into per-source files.
	fmt.Println("\n" + strings.Repeat("-", 60))
	fmt.Println("Part 2: URL Source with Docling Extractor (PDF/HTML -> Markdown -> Chunked Files)")
	fmt.Println(strings.Repeat("-", 60))

	demonstrateKnowledgeSource(ctx, ext, demoURLs, *outputDir)

	fmt.Println("\nDone!")
}

// demonstrateKnowledgeSource shows how to use url.WithExtractor to produce
// chunked documents directly and write chunks from the same source into one file.
func demonstrateKnowledgeSource(ctx context.Context, ext extractor.Extractor, urls []string, outputDir string) {
	fmt.Println("\nCreating URL knowledge source with Docling extractor...")
	fmt.Println("  Using: url.WithExtractor(doclingExtractor)")

	src := urlsource.New(
		urls,
		urlsource.WithName("docling-extracted-urls"),
		urlsource.WithExtractor(ext),
		urlsource.WithMetadataValue("extractor", "docling"),
		urlsource.WithChunkSize(500),
		urlsource.WithChunkOverlap(50),
	)

	fmt.Println("  Reading chunked documents directly from URL source...")
	startTime := time.Now()
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		log.Printf("  Read failed: %v", err)
		return
	}
	fmt.Printf("  Read time:  %v\n", time.Since(startTime))
	fmt.Printf("  Chunks:     %d\n", len(docs))

	chunkDir := filepath.Join(outputDir, "chunked")
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		log.Printf("  Failed to create chunk output dir: %v", err)
		return
	}

	groupedDocs := groupDocumentsBySourceURL(docs)
	for _, rawURL := range urls {
		groupDocs := groupedDocs[rawURL]
		if len(groupDocs) == 0 {
			log.Printf("  No chunks generated for %s", rawURL)
			continue
		}

		fileName := outputBaseNameFromURL(rawURL) + "_chunks.md"
		filePath := filepath.Join(chunkDir, fileName)
		content := buildGroupedChunkFileContent(rawURL, groupDocs)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			log.Printf("  Failed to write %s: %v", filePath, err)
			continue
		}

		fmt.Printf("  Source: %s\n", rawURL)
		fmt.Printf("  Chunks: %d\n", len(groupDocs))
		fmt.Printf("  Output: %s\n", filePath)
		printPreview(groupDocs[0].Content, 200)
	}
}

func buildGroupedChunkFileContent(rawURL string, docs []anyDocument) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Chunked Output for %s\n\n", rawURL))
	b.WriteString(fmt.Sprintf("- Total Chunks: %d\n\n", len(docs)))

	for i, doc := range docs {
		b.WriteString("-----\n")
		b.WriteString(fmt.Sprintf("Chunk %03d\n", i+1))
		b.WriteString(fmt.Sprintf("Name: %s\n", doc.Name))
		b.WriteString("Metadata:\n")

		keys := make([]string, 0, len(doc.Metadata))
		for k := range doc.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("  - %s: %v\n", k, doc.Metadata[k]))
		}

		b.WriteString("-----\n\n")
		b.WriteString(doc.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}

type anyDocument struct {
	Name     string
	Content  string
	Metadata map[string]any
	URL      string
}

func groupDocumentsBySourceURL(docs []*document.Document) map[string][]anyDocument {
	grouped := make(map[string][]anyDocument)
	for _, doc := range docs {
		rawURL, _ := doc.Metadata["trpc_agent_go_url"].(string)
		grouped[rawURL] = append(grouped[rawURL], anyDocument{
			Name:     doc.Name,
			Content:  doc.Content,
			Metadata: doc.Metadata,
			URL:      rawURL,
		})
	}
	return grouped
}

func downloadURL(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trpc-agent-go/docling-example")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func outputBaseNameFromURL(rawURL string) string {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "document"
	}
	base := filepath.Base(strings.TrimSuffix(parsed.Path, "/"))
	if base == "" || base == "." || base == "/" {
		if parsed.Host != "" {
			return parsed.Host
		}
		return "document"
	}
	return base
}

// printPreview prints the first n characters of content with indentation.
func printPreview(content string, n int) {
	if len(content) > n {
		content = content[:n] + "..."
	}
	lines := strings.Split(content, "\n")
	maxLines := 15
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "  ...")
	}
	for _, line := range lines {
		fmt.Printf("    %s\n", line)
	}
}
