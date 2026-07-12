//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"regexp"
	"strings"
)

// UnclosedResourceRule detects file/socket/DB resources that are
// opened but not closed with defer.
type UnclosedResourceRule struct{}

func (r *UnclosedResourceRule) ID() string       { return "UNCLOSED_RESOURCE" }
func (r *UnclosedResourceRule) Category() string { return "resource_leak" }
func (r *UnclosedResourceRule) Description() string {
	return "Detects os.Open, sql.Open, net.Dial and similar calls without " +
		"a matching defer Close()"
}

var (
	reOsOpen    = regexp.MustCompile(`(os\.Open|os\.OpenFile|os\.Create)\s*\(`)
	reSqlOpen   = regexp.MustCompile(`sql\.Open\s*\(`)
	reNetDial   = regexp.MustCompile(`net\.Dial\s*\(`)
	reNetListen = regexp.MustCompile(`net\.Listen\s*\(`)
)

// trackOpenLines records lines where a resource is opened so the
// rule can look ahead in the hunk for a matching defer Close.
// Since we operate line-by-line, we use a heuristic: flag the open
// if the same line or next line doesn't have defer Close.

func (r *UnclosedResourceRule) Check(file DiffFile, hunk DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	isOpen := reOsOpen.MatchString(content) ||
		reSqlOpen.MatchString(content) ||
		reNetDial.MatchString(content) ||
		reNetListen.MatchString(content)

	if !isOpen {
		return nil
	}

	// Check if the same line or a nearby line has defer .Close().
	// Look at the next 5 lines in the hunk for a defer Close.
	hasDeferClose := false
	if strings.Contains(content, "defer") && strings.Contains(content, "Close") {
		hasDeferClose = true
	}
	if !hasDeferClose {
		// Scan next few lines in the hunk.
		idx := -1
		for i, l := range hunk.Lines {
			if l.Number == line.Number && l.Type == line.Type {
				idx = i
				break
			}
		}
		if idx >= 0 {
			for j := idx + 1; j < len(hunk.Lines) && j <= idx+5; j++ {
				c := hunk.Lines[j].Content
				if strings.Contains(c, "defer") &&
					strings.Contains(c, "Close") {
					hasDeferClose = true
					break
				}
			}
		}
	}

	if !hasDeferClose {
		findings = append(findings, Finding{
			Severity: SeverityHigh,
			Title:    "Resource opened without defer Close()",
			Evidence: content,
			Recommendation: "Add `defer resource.Close()` immediately after " +
				"opening to ensure the resource is released even on error paths.",
			Confidence: 0.85,
		})
	}

	return findings
}

// HTTPBodyNotClosedRule detects http.Response.Body that is not
// closed with defer.
type HTTPBodyNotClosedRule struct{}

func (r *HTTPBodyNotClosedRule) ID() string       { return "HTTP_BODY_NOT_CLOSED" }
func (r *HTTPBodyNotClosedRule) Category() string { return "resource_leak" }
func (r *HTTPBodyNotClosedRule) Description() string {
	return "Detects HTTP response bodies that are not closed with defer"
}

var (
	reHTTPClient = regexp.MustCompile(`http\.Client|httpClient|client\.Do|client\.Get|client\.Post|http\.Get|http\.Post`)
	reRespBody   = regexp.MustCompile(`resp\.Body|response\.Body|res\.Body`)
)

func (r *HTTPBodyNotClosedRule) Check(file DiffFile, hunk DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reHTTPClient.MatchString(content) && !strings.Contains(content, "defer") {
		// Check if a subsequent line has defer resp.Body.Close()
		hasDeferBodyClose := false
		idx := -1
		for i, l := range hunk.Lines {
			if l.Number == line.Number && l.Type == line.Type {
				idx = i
				break
			}
		}
		if idx >= 0 {
			for j := idx + 1; j < len(hunk.Lines) && j <= idx+8; j++ {
				c := hunk.Lines[j].Content
				if strings.Contains(c, "defer") &&
					strings.Contains(c, "Body") &&
					strings.Contains(c, "Close") {
					hasDeferBodyClose = true
					break
				}
			}
		}
		if !hasDeferBodyClose {
			findings = append(findings, Finding{
				Severity: SeverityHigh,
				Title:    "HTTP response body not closed",
				Evidence: content,
				Recommendation: "Add `defer resp.Body.Close()` after the HTTP " +
					"call to prevent connection leaks.",
				Confidence: 0.85,
			})
		}
	}

	// Direct use of resp.Body without defer Close.
	if reRespBody.MatchString(content) &&
		!strings.Contains(content, "defer") &&
		!strings.Contains(content, "Close") {
		findings = append(findings, Finding{
			Severity: SeverityMedium,
			Title:    "HTTP body accessed without defer Close",
			Evidence: content,
			Recommendation: "Ensure `defer resp.Body.Close()` is called after " +
				"the HTTP response is received.",
			Confidence: 0.6,
		})
	}

	return findings
}
