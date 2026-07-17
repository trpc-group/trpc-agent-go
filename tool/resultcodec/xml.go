//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonx"
)

const (
	xmlRootElement = "result"
	xmlItemElement = "item"
)

// XML returns a Codec that encodes the result as XML. The encoder maps the JSON
// logical representation of the result to XML, so XML and JSON preserve the same
// logical fields and values without adding business semantics.
//
// Mapping rules (stable and suitable for golden tests):
//   - The document root is <result>.
//   - A JSON object becomes one child element per key, in ascending key order.
//     A key that is a valid XML element name is used as the tag; otherwise the
//     generic element <item key="KEY"> is used.
//   - A JSON array becomes a sequence of <item> elements.
//   - Strings, numbers, and booleans become escaped text; numbers keep their
//     JSON textual form.
//   - null becomes an empty element.
//
// Output is valid UTF-8 with format-special characters escaped.
func XML() Codec {
	return xmlCodec{}
}

type xmlCodec struct{}

// Encode serializes the result as XML derived from its JSON logical tree. A
// panic from a business MarshalJSON (via the JSON logical tree) is recovered and
// returned as an error.
func (xmlCodec) Encode(_ context.Context, result any) (s string, err error) {
	defer recoverCodecPanic("XML", &err)
	tree, tErr := jsonLogicalTree(result)
	if tErr != nil {
		return "", tErr
	}
	var buf bytes.Buffer
	if wErr := writeXMLElement(&buf, xmlRootElement, "", tree); wErr != nil {
		return "", wErr
	}
	return buf.String(), nil
}

// jsonLogicalTree renders result to JSON (matching the default encoding) and
// parses it back into a generic tree of map[string]any, []any, json.Number,
// string, bool, or nil. Routing through JSON guarantees XML and JSON share the
// same logical fields and values.
func jsonLogicalTree(result any) (any, error) {
	b, err := jsonx.MarshalNoHTMLEscape(result)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// writeXMLElement writes a single element with the given name and optional key
// attribute wrapping the encoded value. A nil value is written as an empty
// element.
func writeXMLElement(buf *bytes.Buffer, name, keyAttr string, value any) error {
	buf.WriteByte('<')
	buf.WriteString(name)
	if keyAttr != "" {
		buf.WriteString(` key="`)
		if err := escapeXMLText(buf, keyAttr); err != nil {
			return err
		}
		buf.WriteByte('"')
	}
	if value == nil {
		buf.WriteString("></")
		buf.WriteString(name)
		buf.WriteByte('>')
		return nil
	}
	buf.WriteByte('>')
	if err := writeXMLValue(buf, value); err != nil {
		return err
	}
	buf.WriteString("</")
	buf.WriteString(name)
	buf.WriteByte('>')
	return nil
}

// writeXMLValue writes the body of an element for a JSON logical value.
func writeXMLValue(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			name, attr := xmlElementName(k)
			if err := writeXMLElement(buf, name, attr, v[k]); err != nil {
				return err
			}
		}
	case []any:
		for _, elem := range v {
			if err := writeXMLElement(buf, xmlItemElement, "", elem); err != nil {
				return err
			}
		}
	case json.Number:
		return escapeXMLText(buf, v.String())
	case string:
		return escapeXMLText(buf, v)
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	default:
		return fmt.Errorf("resultcodec: XML cannot encode value of type %T", value)
	}
	return nil
}

// escapeXMLText validates s for XML 1.0-legal characters and writes the escaped
// text. Unlike xml.EscapeText alone, it rejects XML-illegal runes (for example
// control characters that a JSON value can carry) with an error instead of
// silently replacing them with U+FFFD, which would lose data.
func escapeXMLText(buf *bytes.Buffer, s string) error {
	if err := validateXMLText(s); err != nil {
		return err
	}
	return xml.EscapeText(buf, []byte(s))
}

// validateXMLText returns an error if s contains a rune that is illegal in XML.
func validateXMLText(s string) error {
	for _, r := range s {
		if !isValidXMLChar(r) {
			return fmt.Errorf(
				"resultcodec: XML cannot encode illegal character %#U",
				r,
			)
		}
	}
	return nil
}

// isValidXMLChar reports whether r is allowed by the XML 1.0 Char production.
func isValidXMLChar(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// xmlElementName returns the element name for a JSON object key. A key that is a
// valid XML name is used directly; otherwise the generic <item> element with a
// key attribute carries the original key.
func xmlElementName(key string) (name, keyAttr string) {
	if isValidXMLName(key) {
		return key, ""
	}
	return xmlItemElement, key
}

// isValidXMLName reports whether s is a valid XML element name for the subset of
// names this codec emits directly (namespaces are excluded).
func isValidXMLName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isXMLNameStart(r) {
				return false
			}
			continue
		}
		if !isXMLNameChar(r) {
			return false
		}
	}
	return true
}

func isXMLNameStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isXMLNameChar(r rune) bool {
	return r == '_' || r == '-' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
