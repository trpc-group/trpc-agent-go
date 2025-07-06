// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"regexp"
	"strings"
)

// Strategy defines the interface for document chunking strategies.
type Strategy interface {
	// Chunk splits a document into smaller chunks based on the strategy's algorithm.
	Chunk(doc interface{}) ([]interface{}, error)
}

var (
	// cleanTextRegex removes extra whitespace and normalizes line breaks.
	cleanTextRegex = regexp.MustCompile(`\s+`)
)

// cleanText normalizes whitespace in text content.
func cleanText(content string) string {
	// Trim leading and trailing whitespace.
	content = strings.TrimSpace(content)

	// Normalize line breaks.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc interface{}, content string, chunkNumber int) interface{} {
	// This is a placeholder function for the chunking package.
	// The actual implementation should be in the document package.
	return nil
}

// itoa converts an integer to a string (simple implementation).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var result []byte
	negative := i < 0
	if negative {
		i = -i
	}

	for i > 0 {
		result = append([]byte{byte('0' + i%10)}, result...)
		i /= 10
	}

	if negative {
		result = append([]byte{'-'}, result...)
	}
	return string(result)
}
