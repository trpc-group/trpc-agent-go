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
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
)

// MarkdownChunking implements a chunking strategy optimized for markdown documents.
type MarkdownChunking struct {
	chunkSize int
	overlap   int
	md        goldmark.Markdown
}

// MarkdownOption represents a functional option for configuring MarkdownChunking.
type MarkdownOption func(*MarkdownChunking)

// WithMarkdownChunkSize sets the maximum size of each chunk in characters.
func WithMarkdownChunkSize(size int) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.chunkSize = size
	}
}

// WithMarkdownOverlap sets the number of characters to overlap between chunks.
func WithMarkdownOverlap(overlap int) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.overlap = overlap
	}
}

// NewMarkdownChunking creates a new markdown chunking strategy with options.
func NewMarkdownChunking(opts ...MarkdownOption) *MarkdownChunking {
	mc := &MarkdownChunking{
		chunkSize: defaultChunkSize,
		overlap:   defaultOverlap,
		md:        goldmark.New(),
	}
	// Apply options.
	for _, opt := range opts {
		opt(mc)
	}
	// Validate parameters.
	if mc.overlap >= mc.chunkSize {
		mc.overlap = min(defaultOverlap, mc.chunkSize-1)
	}
	return mc
}

// Chunk splits the document using markdown-aware chunking.
func (m *MarkdownChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)

	// If content is small enough, return as single chunk.
	if encoding.RuneCount(content) <= m.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	// Parse markdown structure using proper parser.
	sections := m.parseMarkdownSections(content)

	// Create chunks based on sections.
	chunks := m.createChunksFromSections(sections, doc)

	// Apply overlap if specified.
	if m.overlap > 0 {
		chunks = m.applyOverlap(chunks)
	}

	return chunks, nil
}

// markdownSection represents a section in a markdown document.
type markdownSection struct {
	Level   int    // Header level (0 for no header)
	Title   string // Section title
	Content string // Section content
	Start   int    // Start position in original text
	End     int    // End position in original text
	Type    string // Section type (header, paragraph, list, code_block, etc.)
}

// parseMarkdownSections parses the markdown content into sections using proper parser.
func (m *MarkdownChunking) parseMarkdownSections(content string) []markdownSection {
	// Create a new parser context.
	reader := text.NewReader([]byte(content))
	doc := m.md.Parser().Parse(reader)
	source := []byte(content)

	var sections []markdownSection
	var currentSection markdownSection
	var currentContent strings.Builder
	position := 0

	// Walk through the AST to extract sections.
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch n := node.(type) {
		case *ast.Heading:
			// Save previous section if it exists
			if currentSection.Level > 0 {
				currentSection.Content = strings.TrimSpace(currentContent.String())
				currentSection.End = position
				sections = append(sections, currentSection)
			}

			// Extract header title.
			title := m.extractText(n, source)
			level := n.Level

			// Start new section.
			currentSection = markdownSection{
				Level:   level,
				Title:   title,
				Content: "",
				Start:   position,
				Type:    "header",
			}
			currentContent.Reset()

		case *ast.Paragraph:
			if currentSection.Level == 0 {
				// This is a paragraph without a header.
				currentSection.Type = "paragraph"
			}
			// Add paragraph content.
			paraText := m.extractText(n, source)
			if currentContent.Len() > 0 {
				currentContent.WriteString("\n\n")
			}
			currentContent.WriteString(paraText)

		case *ast.List:
			if currentSection.Level == 0 {
				currentSection.Type = "list"
			}
			// Add list content.
			listText := m.extractText(n, source)
			if currentContent.Len() > 0 {
				currentContent.WriteString("\n\n")
			}
			currentContent.WriteString(listText)

		case *ast.FencedCodeBlock:
			if currentSection.Level == 0 {
				currentSection.Type = "code_block"
			}
			// Add code block content.
			codeText := m.extractText(n, source)
			if currentContent.Len() > 0 {
				currentContent.WriteString("\n\n")
			}
			currentContent.WriteString(codeText)

		case *ast.Blockquote:
			if currentSection.Level == 0 {
				currentSection.Type = "blockquote"
			}
			// Add blockquote content.
			quoteText := m.extractText(n, source)
			if currentContent.Len() > 0 {
				currentContent.WriteString("\n\n")
			}
			currentContent.WriteString(quoteText)
		}

		position += len(m.extractText(node, source))
		return ast.WalkContinue, nil
	})

	// Add the last section.
	if currentContent.Len() > 0 {
		currentSection.Content = strings.TrimSpace(currentContent.String())
		currentSection.End = len(content)
		sections = append(sections, currentSection)
	} else if currentSection.Level > 0 {
		// Handle case where we have headers but no content
		currentSection.Content = ""
		currentSection.End = len(content)
		sections = append(sections, currentSection)
	}

	// If no sections found, treat entire content as one section
	if len(sections) == 0 {
		sections = append(sections, markdownSection{
			Level:   0,
			Title:   "",
			Content: content,
			Start:   0,
			End:     len(content),
			Type:    "content",
		})
	}

	return sections
}

// extractText extracts text content from an AST node.
func (m *MarkdownChunking) extractText(node ast.Node, source []byte) string {
	var buf bytes.Buffer
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch v := n.(type) {
		case *ast.Text:
			buf.Write(v.Text(source))
		case *ast.String:
			buf.Write(v.Value)
		}
		return ast.WalkContinue, nil
	})
	return buf.String()
}

// createChunksFromSections creates document chunks based on markdown sections.
func (m *MarkdownChunking) createChunksFromSections(
	sections []markdownSection,
	originalDoc *document.Document,
) []*document.Document {
	var chunks []*document.Document
	chunkNumber := 1

	for _, section := range sections {
		// For headers without content, still create a chunk with just the header
		chunkContent := m.formatSection(section)

		// If section is small enough, create a single chunk.
		if encoding.RuneCount(section.Content) <= m.chunkSize {
			chunk := createChunk(originalDoc, chunkContent, chunkNumber)
			chunks = append(chunks, chunk)
			chunkNumber++
			continue
		}

		// Split large sections into smaller chunks.
		sectionChunks := m.splitLargeSection(section, originalDoc, chunkNumber)
		chunks = append(chunks, sectionChunks...)
		chunkNumber += len(sectionChunks)
	}

	return chunks
}

// formatSection formats a markdown section for chunking.
func (m *MarkdownChunking) formatSection(section markdownSection) string {
	var result strings.Builder

	// Add header if present.
	if section.Level > 0 {
		headerPrefix := strings.Repeat("#", section.Level)
		result.WriteString(headerPrefix)
		result.WriteString(" ")
		result.WriteString(section.Title)
		result.WriteString("\n\n")
	}

	// Add content.
	result.WriteString(section.Content)

	return result.String()
}

// splitLargeSection splits a large section into smaller chunks.
func (m *MarkdownChunking) splitLargeSection(
	section markdownSection,
	originalDoc *document.Document,
	startChunkNumber int,
) []*document.Document {
	var chunks []*document.Document
	content := section.Content
	chunkNumber := startChunkNumber

	// Try to split by paragraphs first for better readability
	paragraphs := strings.Split(content, "\n\n")

	// If we have multiple paragraphs, try to group them intelligently
	if len(paragraphs) > 1 {
		var currentChunk strings.Builder
		currentSize := 0

		for _, paragraph := range paragraphs {
			paragraphSize := encoding.RuneCount(paragraph)

			// If adding this paragraph would exceed chunk size, create a new chunk
			if currentSize+paragraphSize > m.chunkSize && currentSize > 0 {
				chunkContent := m.formatSectionWithHeader(section, currentChunk.String())
				chunk := createChunk(originalDoc, chunkContent, chunkNumber)
				chunks = append(chunks, chunk)

				chunkNumber++
				currentChunk.Reset()
				currentSize = 0
			}

			// If single paragraph is too large, split it with fixed-size chunking
			if paragraphSize > m.chunkSize {
				// Save current chunk if it has content
				if currentChunk.Len() > 0 {
					chunkContent := m.formatSectionWithHeader(section, currentChunk.String())
					chunk := createChunk(originalDoc, chunkContent, chunkNumber)
					chunks = append(chunks, chunk)
					chunkNumber++
					currentChunk.Reset()
					currentSize = 0
				}

				// Split the large paragraph into fixed-size chunks
				paraChunks := encoding.SafeSplitBySize(paragraph, m.chunkSize)
				for _, paraChunk := range paraChunks {
					chunkContent := m.formatSectionWithHeader(section, paraChunk)
					chunk := createChunk(originalDoc, chunkContent, chunkNumber)
					chunks = append(chunks, chunk)
					chunkNumber++
				}
			} else {
				// Add paragraph to current chunk
				if currentChunk.Len() > 0 {
					currentChunk.WriteString("\n\n")
				}
				currentChunk.WriteString(paragraph)
				currentSize += paragraphSize
			}
		}

		// Add the last chunk if there's content
		if currentChunk.Len() > 0 {
			chunkContent := m.formatSectionWithHeader(section, currentChunk.String())
			chunk := createChunk(originalDoc, chunkContent, chunkNumber)
			chunks = append(chunks, chunk)
		}
	} else {
		// Single "paragraph" or no paragraph structure - use fixed-size chunking
		textChunks := encoding.SafeSplitBySize(content, m.chunkSize)
		for _, chunkText := range textChunks {
			chunkContent := m.formatSectionWithHeader(section, chunkText)
			chunk := createChunk(originalDoc, chunkContent, chunkNumber)
			chunks = append(chunks, chunk)
			chunkNumber++
		}
	}

	return chunks
}

// formatSectionWithHeader formats a section chunk with its header.
func (m *MarkdownChunking) formatSectionWithHeader(
	section markdownSection, content string) string {
	var result strings.Builder

	// Add header if present.
	if section.Level > 0 {
		headerPrefix := strings.Repeat("#", section.Level)
		result.WriteString(headerPrefix)
		result.WriteString(" ")
		result.WriteString(section.Title)
		result.WriteString("\n\n")
	}

	// Add content.
	result.WriteString(content)

	return result.String()
}

// applyOverlap applies overlap between consecutive chunks.
func (m *MarkdownChunking) applyOverlap(chunks []*document.Document) []*document.Document {
	if len(chunks) <= 1 {
		return chunks
	}

	overlappedChunks := []*document.Document{chunks[0]}

	for i := 1; i < len(chunks); i++ {
		prevText := chunks[i-1].Content
		if encoding.RuneCount(prevText) > m.overlap {
			prevText = encoding.SafeOverlap(prevText, m.overlap)
		}

		// Create new metadata for overlapped chunk.
		metadata := make(map[string]any)
		for k, v := range chunks[i].Metadata {
			metadata[k] = v
		}

		overlappedContent := prevText + chunks[i].Content
		overlappedChunk := &document.Document{
			ID:        chunks[i].ID,
			Name:      chunks[i].Name,
			Content:   overlappedContent,
			Metadata:  metadata,
			CreatedAt: chunks[i].CreatedAt,
			UpdatedAt: chunks[i].UpdatedAt,
		}
		overlappedChunks = append(overlappedChunks, overlappedChunk)
	}
	return overlappedChunks
}
