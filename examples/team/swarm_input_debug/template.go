//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

type childInputTemplateData struct {
	Input     string
	FromAgent string
	ToAgent   string
}

func renderChildInput(tmpl string, data childInputTemplateData) (string, error) {
	parsed, err := template.New("child-input").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse child template: %w", err)
	}
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render child template: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}
