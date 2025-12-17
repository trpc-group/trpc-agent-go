//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package outputformat provides helpers for parsing and validating the common
// output_format config block used by builtin.llmagent and related tooling.
package outputformat

import (
	"fmt"
	"strings"
)

const (
	TypeText = "text"
	TypeJSON = "json"
)

type Spec struct {
	Type   string
	Schema map[string]any
}

// Parse validates output_format and returns a normalized Spec.
// It is strict: passing nil or a non-object will return an error.
//
// Supported shape:
//
//	{
//	  "type": "text" | "json", // optional, defaults to "text"
//	  "schema": { ... }        // optional, must be an object when present
//	}
func Parse(outputFormat any) (Spec, error) {
	ofMap, ok := outputFormat.(map[string]any)
	if !ok || ofMap == nil {
		return Spec{}, fmt.Errorf("output_format must be an object")
	}

	formatType := TypeText
	if t, ok := ofMap["type"]; ok {
		typeStr, ok := t.(string)
		if !ok {
			return Spec{}, fmt.Errorf("output_format.type must be a string when present")
		}
		typeStr = strings.TrimSpace(typeStr)
		if typeStr != "" {
			formatType = typeStr
		}
	}

	if formatType != TypeText && formatType != TypeJSON {
		return Spec{}, fmt.Errorf("output_format.type must be one of: text, json")
	}

	var schema map[string]any
	if rawSchema, ok := ofMap["schema"]; ok {
		s, ok := rawSchema.(map[string]any)
		if !ok {
			return Spec{}, fmt.Errorf("output_format.schema must be an object when present")
		}
		schema = s
	}

	return Spec{
		Type:   formatType,
		Schema: schema,
	}, nil
}

// ParseOptional is a lenient variant of Parse: nil returns the default spec.
func ParseOptional(outputFormat any) (Spec, error) {
	if outputFormat == nil {
		return Spec{Type: TypeText}, nil
	}
	return Parse(outputFormat)
}

// StructuredSchema returns the output_format.schema only when type == "json".
// Invalid/absent output_format returns nil.
func StructuredSchema(outputFormat any) map[string]any {
	spec, err := ParseOptional(outputFormat)
	if err != nil {
		return nil
	}
	if spec.Type != TypeJSON {
		return nil
	}
	return spec.Schema
}
