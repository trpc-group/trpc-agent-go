//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

type pdfPageRange struct {
	FirstPage int
	LastPage  int
	Count     int
}

func pdfPageCount(raw []byte) (int, error) {
	reader, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return 0, fmt.Errorf("failed to create PDF reader: %w", err)
	}
	return reader.NumPage(), nil
}

func resolvePDFPageRange(raw string, totalPages int) (pdfPageRange, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return pdfPageRange{}, fmt.Errorf("Invalid pages parameter: %q. Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed.", raw)
	}
	if strings.HasSuffix(trimmed, "-") {
		firstPage, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(trimmed, "-")))
		if err != nil || firstPage < 1 {
			return pdfPageRange{}, fmt.Errorf("Invalid pages parameter: %q. Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed.", raw)
		}
		if totalPages > 0 && firstPage > totalPages {
			return pdfPageRange{}, fmt.Errorf("Page range %q is outside the PDF page count of %d.", raw, totalPages)
		}
		lastPage := totalPages
		if lastPage < firstPage {
			lastPage = firstPage
		}
		return validatedPDFPageRange(raw, firstPage, lastPage)
	}
	if dashIndex := strings.Index(trimmed, "-"); dashIndex >= 0 {
		firstPage, firstErr := strconv.Atoi(strings.TrimSpace(trimmed[:dashIndex]))
		lastPage, lastErr := strconv.Atoi(strings.TrimSpace(trimmed[dashIndex+1:]))
		if firstErr != nil || lastErr != nil || firstPage < 1 || lastPage < firstPage {
			return pdfPageRange{}, fmt.Errorf("Invalid pages parameter: %q. Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed.", raw)
		}
		if totalPages > 0 && lastPage > totalPages {
			return pdfPageRange{}, fmt.Errorf("Page range %q exceeds the PDF page count of %d.", raw, totalPages)
		}
		return validatedPDFPageRange(raw, firstPage, lastPage)
	}
	page, err := strconv.Atoi(trimmed)
	if err != nil || page < 1 {
		return pdfPageRange{}, fmt.Errorf("Invalid pages parameter: %q. Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed.", raw)
	}
	if totalPages > 0 && page > totalPages {
		return pdfPageRange{}, fmt.Errorf("Page %d exceeds the PDF page count of %d.", page, totalPages)
	}
	return validatedPDFPageRange(raw, page, page)
}

func validatedPDFPageRange(raw string, firstPage int, lastPage int) (pdfPageRange, error) {
	count := lastPage - firstPage + 1
	if count > pdfMaxPagesPerRead {
		return pdfPageRange{}, fmt.Errorf("Page range %q exceeds maximum of %d pages per request. Please use a smaller range.", raw, pdfMaxPagesPerRead)
	}
	return pdfPageRange{
		FirstPage: firstPage,
		LastPage:  lastPage,
		Count:     count,
	}, nil
}

func pdftoppmBinary() (string, error) {
	pdftoppmOnce.Do(func() {
		path, err := pdftoppmLookPath("pdftoppm")
		if err == nil {
			pdftoppmPath = path
		}
	})
	if strings.TrimSpace(pdftoppmPath) == "" {
		return "", fmt.Errorf("pdftoppm is not installed. Install poppler-utils (e.g. `brew install poppler` or `apt-get install poppler-utils`) to enable PDF page rendering.")
	}
	return pdftoppmPath, nil
}

func extractPDFPages(
	filePath string,
	pageRange pdfPageRange,
) (string, int, error) {
	pdftoppmPath, err := pdftoppmBinary()
	if err != nil {
		return "", 0, err
	}
	outputDir, err := os.MkdirTemp("", "claudecode-pdf-*")
	if err != nil {
		return "", 0, err
	}
	outputPrefix := filepath.Join(outputDir, "page")
	args := []string{
		"-jpeg",
		"-f", strconv.Itoa(pageRange.FirstPage),
		"-l", strconv.Itoa(pageRange.LastPage),
		filePath,
		outputPrefix,
	}
	output, err := exec.Command(pdftoppmPath, args...).CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(outputDir)
		message := strings.TrimSpace(string(output))
		if message == "" {
			return "", 0, fmt.Errorf("failed to extract PDF pages: %w", err)
		}
		return "", 0, fmt.Errorf("failed to extract PDF pages: %s", message)
	}
	imageFiles, err := filepath.Glob(outputPrefix + "-*.jpg")
	if err != nil {
		_ = os.RemoveAll(outputDir)
		return "", 0, err
	}
	sort.Strings(imageFiles)
	if len(imageFiles) == 0 {
		_ = os.RemoveAll(outputDir)
		return "", 0, fmt.Errorf("failed to extract PDF pages: no rendered page images were produced")
	}
	return outputDir, len(imageFiles), nil
}
