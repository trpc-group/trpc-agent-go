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
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	stdunicode "unicode"

	"golang.org/x/net/html"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func normalizePath(baseDir string, raw string) (string, string, error) {
	pathValue := strings.TrimSpace(raw)
	if pathValue == "" {
		return "", "", fmt.Errorf("path is required")
	}
	cleanBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", "", err
	}
	if filepath.IsAbs(pathValue) {
		cleanPath := filepath.Clean(pathValue)
		rel, err := filepath.Rel(cleanBase, cleanPath)
		if err != nil {
			return "", "", err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("path is outside base_dir: %s", raw)
		}
		return filepath.ToSlash(filepath.Clean(rel)), cleanPath, nil
	}
	cleanPath := filepath.Clean(pathValue)
	if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path is outside base_dir: %s", raw)
	}
	absPath := filepath.Join(cleanBase, cleanPath)
	return filepath.ToSlash(filepath.Clean(cleanPath)), absPath, nil
}

func (r *runtime) currentBaseDir() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.baseDir
}

func (r *runtime) setBaseDir(baseDir string) {
	r.mu.Lock()
	r.baseDir = baseDir
	r.mu.Unlock()
}

func relativePath(baseDir string, absPath string) string {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(absPath))
	}
	rel, err := filepath.Rel(baseAbs, absPath)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(absPath))
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

func readHTTPBody(
	resp *http.Response,
	maxContentLength int,
	maxTotalContentLength int,
) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	limit := maxContentLength
	if maxTotalContentLength > 0 && (limit == 0 || maxTotalContentLength < limit) {
		limit = maxTotalContentLength
	}
	if limit <= 0 {
		limit = 1 << 20
	}
	reader := io.LimitReader(resp.Body, int64(limit)+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("response body exceeded limit of %d bytes", limit)
	}
	return body, nil
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	parts := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		return len(parts) - 1
	}
	return len(parts)
}

func splitTextLines(content string) []string {
	if content == "" {
		return []string{}
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
}

func sliceLines(content string, offset int, limit *int) (string, int, int) {
	lines := splitTextLines(content)
	totalLines := len(lines)
	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}
	startIdx := startLine - 1
	if startIdx > totalLines {
		startIdx = totalLines
	}
	endIdx := totalLines
	if limit != nil && *limit >= 0 && startIdx+*limit < endIdx {
		endIdx = startIdx + *limit
	}
	sliced := lines[startIdx:endIdx]
	result := strings.Join(sliced, "\n")
	if len(sliced) > 0 && strings.HasSuffix(content, "\n") && endIdx == totalLines {
		result += "\n"
	}
	return result, startLine, totalLines
}

func normalizeNewlines(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return content
}

func detectLineEnding(raw []byte) string {
	if bytes.Contains(raw, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func applyLineEnding(content string, lineEnding string) string {
	if lineEnding == "\r\n" {
		return strings.ReplaceAll(content, "\n", "\r\n")
	}
	return content
}

func decodeTextBytes(raw []byte) (string, string, error) {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		decoder := textunicode.UTF16(textunicode.LittleEndian, textunicode.ExpectBOM).NewDecoder()
		decoded, _, err := transform.String(decoder, string(raw))
		if err != nil {
			return "", "", err
		}
		return normalizeNewlines(decoded), "utf16le", nil
	}
	return normalizeNewlines(string(raw)), "utf8", nil
}

func encodeTextBytes(content string, encoding string, lineEnding string) ([]byte, error) {
	normalized := applyLineEnding(content, lineEnding)
	if encoding == "utf16le" {
		encoder := textunicode.UTF16(textunicode.LittleEndian, textunicode.UseBOM).NewEncoder()
		encoded, _, err := transform.String(encoder, normalized)
		if err != nil {
			return nil, err
		}
		return []byte(encoded), nil
	}
	return []byte(normalized), nil
}

func fileBase64(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func isProbablyBinary(raw []byte) bool {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		return false
	}
	for _, b := range raw {
		if b == 0 {
			return true
		}
	}
	return false
}

func buildStructuredPatch(oldContent string, newContent string) []patchHunk {
	if oldContent == newContent {
		return nil
	}
	oldLines := splitTextLines(oldContent)
	newLines := splitTextLines(newContent)
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldSuffixLimit := len(oldLines) - prefix
	newSuffixLimit := len(newLines) - prefix
	suffix := 0
	for suffix < oldSuffixLimit && suffix < newSuffixLimit {
		if oldLines[len(oldLines)-1-suffix] != newLines[len(newLines)-1-suffix] {
			break
		}
		suffix++
	}
	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]
	lines := make([]string, 0, len(oldMid)+len(newMid))
	for _, line := range oldMid {
		lines = append(lines, "-"+line)
	}
	for _, line := range newMid {
		lines = append(lines, "+"+line)
	}
	oldStart := prefix + 1
	newStart := prefix + 1
	if len(oldLines) == 0 {
		oldStart = 0
	}
	if len(newLines) == 0 {
		newStart = 0
	}
	return []patchHunk{{
		OldStart: oldStart,
		OldLines: len(oldMid),
		NewStart: newStart,
		NewLines: len(newMid),
		Lines:    lines,
	}}
}

func matchSearchDomainFilters(
	rawURL string,
	allowed []string,
	blocked []string,
) bool {
	host := searchURLHost(rawURL)
	if host == "" {
		return len(allowed) == 0
	}
	for _, rule := range blocked {
		if matchDomainRule(host, rule) {
			return false
		}
	}
	if len(allowed) == 0 {
		return true
	}
	for _, rule := range allowed {
		if matchDomainRule(host, rule) {
			return true
		}
	}
	return false
}

func searchURLHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func matchDomainRule(host string, rule string) bool {
	cleanRule := strings.ToLower(strings.TrimSpace(rule))
	if cleanRule == "" {
		return false
	}
	if strings.HasPrefix(cleanRule, "*.") {
		suffix := strings.TrimPrefix(cleanRule, "*.")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	return host == cleanRule || strings.HasSuffix(host, "."+cleanRule)
}

func extractHTMLText(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	parts := make([]string, 0, 32)
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			name := strings.ToLower(node.Data)
			if name == "script" || name == "style" || name == "noscript" {
				return
			}
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				parts = append(parts, collapseWhitespace(text))
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func collapseWhitespace(raw string) string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return stdunicode.IsSpace(r)
	})
	return strings.Join(fields, " ")
}

func joinOutput(stdout string, stderr string) string {
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func sortedCopy(items []string) []string {
	out := append([]string{}, items...)
	slices.Sort(out)
	return out
}
