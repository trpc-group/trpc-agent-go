package document

import (
	"fmt"
	"log"
)

// ExampleFixedSizeChunking demonstrates how to use the fixed-size chunking strategy.
func ExampleFixedSizeChunking() {
	// Create a fixed-size chunking strategy with 100 character chunks and 20 character overlap.
	chunker, err := NewFixedSizeChunking(WithChunkSize(100), WithOverlap(20))
	if err != nil {
		log.Fatal(err)
	}

	// Create a sample document.
	doc := &Document{
		ID:   "example-doc-1",
		Name: "Sample Article",
		Content: `This is a sample article with multiple sentences. 
It demonstrates how fixed-size chunking works. The chunker will split the text 
at whitespace boundaries to avoid breaking words in the middle.`,
		Metadata: map[string]interface{}{
			"type":   "article",
			"source": "example",
		},
	}

	// Chunk the document.
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		log.Fatal(err)
	}

	// Display the results.
	fmt.Printf("Original document size: %d characters\n", len(doc.Content))
	fmt.Printf("Number of chunks: %d\n", len(chunks))

	for i, chunk := range chunks {
		fmt.Printf("\nChunk %d (ID: %s):\n", i+1, chunk.ID)
		fmt.Printf("Size: %d characters\n", len(chunk.Content))
		fmt.Printf("Content: %s\n", chunk.Content)
	}

	// Output will vary based on exact whitespace handling.
}

// ExampleNaturalBreakChunking demonstrates how to use the natural break chunking strategy.
func ExampleNaturalBreakChunking() {
	// Create a natural break chunking strategy.
	chunker, err := NewNaturalBreakChunking(WithChunkSize(80), WithOverlap(10))
	if err != nil {
		log.Fatal(err)
	}

	// Create a document with natural break points.
	doc := &Document{
		ID:   "example-doc-2",
		Name: "Technical Documentation",
		Content: `Introduction
This document explains the system architecture.

Implementation Details
The system uses a microservices approach.
Each service is independently deployable.

Conclusion
This architecture provides scalability and maintainability.`,
	}

	// Chunk the document.
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		log.Fatal(err)
	}

	// Display the results.
	fmt.Printf("Document: %s\n", doc.Name)
	fmt.Printf("Number of chunks: %d\n", len(chunks))

	for i, chunk := range chunks {
		fmt.Printf("\nChunk %d:\n", i+1)
		fmt.Printf("Content: %s\n", chunk.Content)
		fmt.Printf("Ends with natural break: %v\n",
			len(chunk.Content) > 0 && (chunk.Content[len(chunk.Content)-1] == '.' ||
				chunk.Content[len(chunk.Content)-1] == '\n'))
	}
}

// ExampleParagraphChunking demonstrates how to use the paragraph-based chunking strategy.
func ExampleParagraphChunking() {
	// Create a paragraph chunking strategy.
	chunker, err := NewParagraphChunking(WithChunkSize(150), WithOverlap(0))
	if err != nil {
		log.Fatal(err)
	}

	// Create a document with clear paragraph structure.
	doc := &Document{
		ID:   "example-doc-3",
		Name: "Research Paper",
		Content: `Abstract
This paper presents a novel approach to document processing using advanced chunking techniques.

Introduction
Document chunking is a critical component in information retrieval systems. The quality of chunking directly impacts the effectiveness of downstream processing tasks.

Methodology
We evaluated three different chunking strategies: fixed-size, natural break, and paragraph-based chunking. Each strategy was tested on a diverse corpus of documents.

Results
Our experiments show that paragraph-based chunking preserves semantic coherence better than other approaches. The results demonstrate significant improvements in information retrieval accuracy.

Conclusion
Paragraph-based chunking offers superior performance for documents with clear structural organization. Future work will explore adaptive chunking strategies.`,
	}

	// Chunk the document.
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		log.Fatal(err)
	}

	// Display the results.
	fmt.Printf("Document: %s\n", doc.Name)
	fmt.Printf("Number of chunks: %d\n", len(chunks))

	for i, chunk := range chunks {
		fmt.Printf("\nChunk %d:\n", i+1)
		fmt.Printf("Size: %d characters\n", len(chunk.Content))
		fmt.Printf("Content preview: %.100s...\n", chunk.Content)

		// Check if chunk contains complete paragraphs.
		paragraphCount := 0
		for _, char := range chunk.Content {
			if char == '\n' {
				paragraphCount++
			}
		}
		fmt.Printf("Contains paragraph breaks: %v\n", paragraphCount > 0)
	}
}

// Example_chunkingComparison demonstrates the differences between the three chunking strategies.
func Example_chunkingComparison() {
	content := `Artificial Intelligence Overview
AI is transforming industries worldwide.

Machine Learning
ML is a subset of AI that focuses on learning from data.

Deep Learning
Deep learning uses neural networks with multiple layers.`

	doc := &Document{
		ID:      "comparison-doc",
		Content: content,
	}

	strategies := map[string]ChunkingStrategy{}

	// Initialize strategies.
	fixed, _ := NewFixedSizeChunking(WithChunkSize(60), WithOverlap(0))
	natural, _ := NewNaturalBreakChunking(WithChunkSize(60), WithOverlap(0))
	paragraph, _ := NewParagraphChunking(WithChunkSize(60), WithOverlap(0))

	strategies["FixedSize"] = fixed
	strategies["NaturalBreak"] = natural
	strategies["Paragraph"] = paragraph

	fmt.Printf("Original content (%d chars):\n%s\n\n", len(content), content)

	// Compare all strategies.
	for name, strategy := range strategies {
		chunks, err := strategy.Chunk(doc)
		if err != nil {
			log.Printf("Error with %s strategy: %v", name, err)
			continue
		}

		fmt.Printf("%s Strategy - %d chunks:\n", name, len(chunks))
		for i, chunk := range chunks {
			fmt.Printf("  Chunk %d: %.50s...\n", i+1, chunk.Content)
		}
		fmt.Println()
	}
}

// Example_withDefaults demonstrates using chunking strategies with default options.
func Example_withDefaults() {
	// Create chunking strategies with default options.
	fixedChunker, _ := NewFixedSizeChunking()
	naturalChunker, _ := NewNaturalBreakChunking()
	paragraphChunker, _ := NewParagraphChunking()

	doc := &Document{
		Content: "Sample content for demonstrating default chunking options.",
	}

	// Use strategies with defaults.
	fixedChunks, _ := fixedChunker.Chunk(doc)
	naturalChunks, _ := naturalChunker.Chunk(doc)
	paragraphChunks, _ := paragraphChunker.Chunk(doc)

	fmt.Printf("Fixed chunks: %d\n", len(fixedChunks))
	fmt.Printf("Natural chunks: %d\n", len(naturalChunks))
	fmt.Printf("Paragraph chunks: %d\n", len(paragraphChunks))
}
