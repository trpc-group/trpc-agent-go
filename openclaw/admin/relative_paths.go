//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"bytes"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

const (
	headerContentLength = "Content-Length"
	headerContentType   = "Content-Type"
	headerLocation      = "Location"

	htmlMediaType = "text/html"

	htmlAttrAction     = "action"
	htmlAttrDataPrefix = "data-"
	htmlAttrFormAction = "formaction"
	htmlAttrHref       = "href"
	htmlAttrSrc        = "src"

	htmlAttrActionSuffix = "-action"
	htmlAttrHrefSuffix   = "-href"
	htmlAttrPathSuffix   = "-path"
	htmlAttrSrcSuffix    = "-src"
	htmlAttrURLSuffix    = "-url"

	rootPath           = "/"
	currentPathSegment = "."
	parentPathSegment  = ".."
)

var htmlDataReferenceAttrSuffixes = []string{
	htmlAttrActionSuffix,
	htmlAttrHrefSuffix,
	htmlAttrPathSuffix,
	htmlAttrSrcSuffix,
	htmlAttrURLSuffix,
}

// wrapRelativeLinks keeps admin navigation working when the
// service is exposed behind a reverse-proxy subpath.
func wrapRelativeLinks(base http.Handler) http.Handler {
	if base == nil {
		return nil
	}
	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		recorder := newBufferedResponseWriter()
		base.ServeHTTP(recorder, r)
		writeRelativeResponse(w, r, recorder)
	})
}

func wrapRelativeLinksFunc(
	handler http.HandlerFunc,
) http.HandlerFunc {
	if handler == nil {
		return nil
	}
	wrapped := wrapRelativeLinks(handler)
	return wrapped.ServeHTTP
}

type bufferedResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header: make(http.Header),
	}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
}

func (w *bufferedResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(p)
}

func (w *bufferedResponseWriter) Flush() {}

func writeRelativeResponse(
	dst http.ResponseWriter,
	req *http.Request,
	src *bufferedResponseWriter,
) {
	status := src.status
	if status == 0 {
		status = http.StatusOK
	}

	header := src.header.Clone()
	location := strings.TrimSpace(header.Get(headerLocation))
	if location != "" {
		header.Set(
			headerLocation,
			relativeRequestReference(requestPath(req), location),
		)
	}

	body := src.body.Bytes()
	rewritten, ok := rewriteHTMLBody(
		requestPath(req),
		header.Get(headerContentType),
		body,
	)
	if ok {
		body = rewritten
	}

	header.Del(headerContentLength)
	copyHeaders(dst.Header(), header)
	dst.WriteHeader(status)
	_, _ = dst.Write(body)
}

func rewriteHTMLBody(
	requestPath string,
	contentType string,
	body []byte,
) ([]byte, bool) {
	if len(body) == 0 || !isHTMLContentType(contentType) {
		return body, false
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return body, false
	}
	rewriteHTMLReferences(doc, requestPath)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return body, false
	}
	return buf.Bytes(), true
}

func isHTMLContentType(contentType string) bool {
	trimmed := strings.TrimSpace(contentType)
	if trimmed == "" {
		return false
	}

	mediaType, _, err := mime.ParseMediaType(trimmed)
	if err != nil {
		return strings.HasPrefix(
			strings.ToLower(trimmed),
			htmlMediaType,
		)
	}
	return strings.EqualFold(mediaType, htmlMediaType)
}

func rewriteHTMLReferences(node *html.Node, requestPath string) {
	if node == nil {
		return
	}

	if node.Type == html.ElementNode {
		for i := range node.Attr {
			if !isHTMLReferenceAttr(node.Attr[i].Key) {
				continue
			}
			node.Attr[i].Val = relativeRequestReference(
				requestPath,
				node.Attr[i].Val,
			)
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		rewriteHTMLReferences(child, requestPath)
	}
}

func isHTMLReferenceAttr(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case htmlAttrAction,
		htmlAttrFormAction,
		htmlAttrHref,
		htmlAttrSrc:
		return true
	default:
		return isHTMLDataReferenceAttr(normalized)
	}
}

func isHTMLDataReferenceAttr(key string) bool {
	if !strings.HasPrefix(key, htmlAttrDataPrefix) {
		return false
	}
	for _, suffix := range htmlDataReferenceAttrSuffixes {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		if len(key) <= len(htmlAttrDataPrefix)+len(suffix) {
			return false
		}
		return true
	}
	return false
}

func relativeRequestReference(requestPath string, rawTarget string) string {
	target, ok := parseRootRelativeURL(rawTarget)
	if !ok {
		return rawTarget
	}

	target.Path = relativePathReference(requestPath, target.Path)
	return target.String()
}

func parseRootRelativeURL(raw string) (*url.URL, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, rootPath) ||
		strings.HasPrefix(trimmed, "//") {
		return nil, false
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || !strings.HasPrefix(parsed.Path, rootPath) {
		return nil, false
	}
	return parsed, true
}

func relativePathReference(requestPath string, targetPath string) string {
	baseParts := splitURLPath(requestBaseDir(requestPath))
	targetParts := splitURLPath(targetPath)

	common := commonPathPrefixLen(baseParts, targetParts)
	parts := make([]string, 0, len(baseParts)-common+
		len(targetParts)-common)
	for i := common; i < len(baseParts); i++ {
		parts = append(parts, parentPathSegment)
	}
	parts = append(parts, targetParts[common:]...)
	if len(parts) == 0 {
		return currentPathSegment
	}
	return strings.Join(parts, rootPath)
}

func requestBaseDir(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return rootPath
	}

	if !strings.HasPrefix(trimmed, rootPath) {
		trimmed = rootPath + trimmed
	}

	cleaned := path.Clean(trimmed)
	if cleaned == rootPath {
		return rootPath
	}
	if strings.HasSuffix(trimmed, rootPath) {
		return cleaned
	}
	return path.Dir(cleaned)
}

func requestPath(req *http.Request) string {
	if req == nil || req.URL == nil {
		return rootPath
	}
	return req.URL.Path
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func splitURLPath(raw string) []string {
	cleaned := path.Clean(raw)
	if cleaned == rootPath {
		return nil
	}
	trimmed := strings.TrimPrefix(cleaned, rootPath)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, rootPath)
}

func commonPathPrefixLen(left []string, right []string) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return i
		}
	}
	return limit
}
