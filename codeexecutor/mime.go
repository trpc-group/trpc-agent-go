package codeexecutor

import (
	"mime"
	"strings"
)

const (
	mimeTextPrefix = "text/"
	mimeAppJSON    = "application/json"
	mimeSuffixJSON = "+json"
)

// IsTextMIME reports whether mimeType describes a text format that is safe
// to inline as UTF-8 text.
func IsTextMIME(mimeType string) bool {
	mt := strings.TrimSpace(mimeType)
	if parsed, _, err := mime.ParseMediaType(mt); err == nil {
		mt = parsed
	}
	if strings.HasPrefix(mt, mimeTextPrefix) {
		return true
	}
	if mt == mimeAppJSON {
		return true
	}
	return strings.HasSuffix(mt, mimeSuffixJSON)
}
