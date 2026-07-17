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

// Encode serializes the result as XML derived from its JSON logical tree.
func (xmlCodec) Encode(_ context.Context, result any) (string, error) {
	tree, err := jsonLogicalTree(result)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := writeXMLElement(&buf, xmlRootElement, "", tree); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// jsonLogicalTree renders result to JSON (matching the default encoding) and
// parses it back into a generic tree of map[string]any, []any, json.Number,
// string, bool, or nil. Routing through JSON guarantees XML and JSON share the
// same logical fields and values.
func jsonLogicalTree(result any) (any, error) {
	b, err := marshalJSONNoHTMLEscape(result)
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
		if err := xml.EscapeText(buf, []byte(keyAttr)); err != nil {
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
		return xml.EscapeText(buf, []byte(v.String()))
	case string:
		return xml.EscapeText(buf, []byte(v))
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
