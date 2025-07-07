// Package source provides internal source utils.
package source

import (
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/csv"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/docx"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/json"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/markdown"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/pdf"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/text"
)

// GetReaders returns all available readers.
func GetReaders() map[string]reader.Reader {
	readers := make(map[string]reader.Reader)
	readers["text"] = text.New()
	readers["pdf"] = pdf.New()
	readers["markdown"] = markdown.New()
	readers["json"] = json.New()
	readers["csv"] = csv.New()
	readers["docx"] = docx.New()
	return readers
}

// GetFileType determines the file type based on the file extension.
func GetFileType(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".txt", ".text":
		return "text"
	case ".pdf":
		return "pdf"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".csv":
		return "csv"
	case ".docx", ".doc":
		return "docx"
	default:
		return "text"
	}
}

// GetFileTypeFromContentType determines the file type based on content type or file extension.
func GetFileTypeFromContentType(contentType, fileName string) string {
	// First try content type.
	if contentType != "" {
		parts := strings.Split(contentType, ";")
		mainType := strings.TrimSpace(parts[0])

		switch {
		case strings.Contains(mainType, "text/html"):
			return "text"
		case strings.Contains(mainType, "text/plain"):
			return "text"
		case strings.Contains(mainType, "application/json"):
			return "json"
		case strings.Contains(mainType, "text/csv"):
			return "csv"
		case strings.Contains(mainType, "application/pdf"):
			return "pdf"
		case strings.Contains(mainType, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"):
			return "docx"
		}
	}

	// Fall back to file extension.
	ext := filepath.Ext(fileName)
	switch ext {
	case ".txt", ".text", ".html", ".htm":
		return "text"
	case ".pdf":
		return "pdf"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".csv":
		return "csv"
	case ".docx", ".doc":
		return "docx"
	default:
		return "text"
	}
}
