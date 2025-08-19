//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package jsonschema provides utilities to generate JSON Schema documents
// from Go types. The generator is recursive and production-ready with
// support for nested structs, arrays/slices, maps, enums, time formats,
// and $defs/$ref for de-duplication and recursion safety.
package jsonschema

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Generator generates JSON Schema from Go types.
type Generator struct {
	mu sync.Mutex
	// visited maps a Go type to a $defs key.
	visited map[reflect.Type]string
	// defs stores schema definitions keyed by definition name.
	defs map[string]map[string]any
	// seq is used to create stable unique definition names when unnamed.
	seq int
	// processing tracks types currently being processed to detect recursion.
	processing map[reflect.Type]bool
	// referenced marks types that were referenced via $ref and thus need $defs.
	referenced map[reflect.Type]bool
}

// New returns a new Generator instance.
func New() *Generator {
	return &Generator{
		visited:    make(map[reflect.Type]string),
		defs:       make(map[string]map[string]any),
		processing: make(map[reflect.Type]bool),
		referenced: make(map[reflect.Type]bool),
	}
}

// Generate returns a JSON schema for the provided type. The returned
// schema may include a $defs section when needed.
func (g *Generator) Generate(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	root := g.toSchema(t)
	if len(g.defs) > 0 {
		// Attach $defs at the root.
		root["$defs"] = g.defs
	}
	return root
}

// kindToJSONType maps simple Go kinds to their JSON Schema type.
var kindToJSONType = map[reflect.Kind]string{
	reflect.Bool:    "boolean",
	reflect.Int:     "integer",
	reflect.Int8:    "integer",
	reflect.Int16:   "integer",
	reflect.Int32:   "integer",
	reflect.Int64:   "integer",
	reflect.Uint:    "integer",
	reflect.Uint8:   "integer",
	reflect.Uint16:  "integer",
	reflect.Uint32:  "integer",
	reflect.Uint64:  "integer",
	reflect.Float32: "number",
	reflect.Float64: "number",
	reflect.String:  "string",
}

func (g *Generator) toSchema(t reflect.Type) map[string]any {
	// Handle pointers by unwrapping to element type.
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Special-case time.Time => string with date-time format.
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{"type": "string", "format": "date-time"}
	}

	if typeName, ok := kindToJSONType[t.Kind()]; ok {
		return map[string]any{"type": typeName}
	}

	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		return g.schemaForArray(t)
	}

	if t.Kind() == reflect.Map {
		return g.schemaForMap(t)
	}

	if t.Kind() == reflect.Struct {
		return g.schemaForStruct(t)
	}

	return map[string]any{"type": "string"}
}

func (g *Generator) schemaForArray(t reflect.Type) map[string]any {
	return map[string]any{
		"type":  "array",
		"items": g.toSchema(t.Elem()),
	}
}

func (g *Generator) schemaForMap(t reflect.Type) map[string]any {
	if t.Key().Kind() == reflect.String {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": g.toSchema(t.Elem()),
		}
	}
	// Fallback: represent as array of key-value pairs.
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "object"},
	}
}

func (g *Generator) schemaForStruct(t reflect.Type) map[string]any {
	// If currently processing this type, return a $ref and mark referenced.
	if g.processing[t] {
		defKey, ok := g.visited[t]
		if !ok {
			defKey = g.definitionName(t)
			g.visited[t] = defKey
		}
		g.referenced[t] = true
		return map[string]any{"$ref": "#/$defs/" + defKey}
	}

	// Ensure a defKey exists for potential recursion.
	defKey, ok := g.visited[t]
	if !ok {
		defKey = g.definitionName(t)
		g.visited[t] = defKey
	}

	g.processing[t] = true
	props := map[string]any{}
	required := make([]string, 0)
	n := t.NumField()
	for i := 0; i < n; i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := fieldJSONName(f)
		if name == "-" || name == "" {
			continue
		}
		fieldSchema := g.toSchema(f.Type)
		applyFieldTags(fieldSchema, f)
		props[name] = fieldSchema
		if !isOmitEmpty(jsonTag) && !isPointerLike(f.Type) {
			required = append(required, name)
		}
	}
	g.processing[t] = false

	obj := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	// If this type was referenced via $ref, materialize its definition.
	if g.referenced[t] {
		g.defs[defKey] = obj
	}
	return obj
}

func applyFieldTags(fieldSchema map[string]any, f reflect.StructField) {
	if desc := strings.TrimSpace(f.Tag.Get("description")); desc != "" {
		fieldSchema["description"] = desc
	}
	if enumTag := strings.TrimSpace(f.Tag.Get("enum")); enumTag != "" {
		parts := strings.Split(enumTag, ",")
		enums := make([]any, 0, len(parts))
		for _, p := range parts {
			enums = append(enums, strings.TrimSpace(p))
		}
		fieldSchema["enum"] = enums
	}
}

func (g *Generator) definitionName(t reflect.Type) string {
	// Prefer package-qualified name when available, else synthesize.
	if t.Name() != "" {
		if t.PkgPath() != "" {
			return sanitizeRefName(t.PkgPath() + "." + t.Name())
		}
		return sanitizeRefName(t.Name())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	return sanitizeRefName("Type" + strconv.Itoa(g.seq))
}

func sanitizeRefName(s string) string {
	// Replace characters that are not friendly in JSON Pointer segments.
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func fieldJSONName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	// Keep name before comma.
	parts := strings.Split(tag, ",")
	if parts[0] == "" {
		return f.Name
	}
	return parts[0]
}

func isOmitEmpty(tag string) bool {
	if tag == "" {
		return false
	}
	parts := strings.Split(tag, ",")
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) == "omitempty" {
			return true
		}
	}
	return false
}

func isPointerLike(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return true
	default:
		return false
	}
}
