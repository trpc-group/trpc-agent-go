//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	registry.MustRegister(&HTTPRequestComponent{})
}

// HTTPRequestComponent is a built-in component for making HTTP requests.
// It is designed primarily for DSL usage and supports simple template-based
// variable interpolation from graph.State.
//
// NOTE: For DSL graphs the Compiler will treat this as a normal component
// (no special NodeFunc like builtin.llm), so Execute is the main entrypoint.
type HTTPRequestComponent struct{}

// Metadata returns the component metadata.
func (c *HTTPRequestComponent) Metadata() registry.ComponentMetadata {
	stringType := reflect.TypeOf("")
	intType := reflect.TypeOf(0)
	mapStringAnyType := reflect.TypeOf(map[string]any{})

	return registry.ComponentMetadata{
		Name:        "builtin.http_request",
		DisplayName: "HTTP Request",
		Description: "Perform an HTTP request with optional template-based config",
		Category:    "Integration",
		Version:     "1.0.0",

		// Logical inputs for documentation / schema purposes.
		Inputs: []registry.ParameterSchema{
			{
				Name:        "url",
				DisplayName: "URL",
				Description: "Final URL after template rendering",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      stringType,
				Required:    false,
			},
			{
				Name:        "body",
				DisplayName: "Body",
				Description: "Final request body after template rendering",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      stringType,
				Required:    false,
			},
		},

		Outputs: []registry.ParameterSchema{
			{
				Name:        "status_code",
				DisplayName: "Status Code",
				Description: "HTTP response status code",
				Type:        "int",
				TypeID:      "number",
				Kind:        "number",
				GoType:      intType,
				Required:    false,
			},
			{
				Name:        "response_body",
				DisplayName: "Response Body",
				Description: "HTTP response body as string",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      stringType,
				Required:    false,
			},
			{
				Name:        "response_headers",
				DisplayName: "Response Headers",
				Description: "HTTP response headers",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
		},

		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "method",
				DisplayName: "HTTP Method",
				Description: "HTTP method to use (GET, POST, etc.)",
				Type:        "string",
				GoType:      stringType,
				Required:    false,
				Default:     "GET",
			},
			{
				Name:        "url_template",
				DisplayName: "URL Template",
				Description: "URL template, supports {{state.xxx}} and {{nodes.id.key}}",
				Type:        "string",
				GoType:      stringType,
				Required:    true,
			},
			{
				Name:        "body_template",
				DisplayName: "Body Template",
				Description: "Body template, supports {{state.xxx}} and {{nodes.id.key}}",
				Type:        "string",
				GoType:      stringType,
				Required:    false,
			},
			{
				Name:        "headers",
				DisplayName: "Headers",
				Description: "HTTP headers (map of name to value), values may contain templates",
				Type:        "map[string]any",
				GoType:      mapStringAnyType,
				Required:    false,
			},
		},
	}
}

// Execute executes the HTTP request component.
// It supports simple template syntax in url_template, body_template and
// header values:
//
//   - {{state.foo}}                           -> state["foo"]
//   - {{nodes.nodeID.key}}                    -> state[node_responses][nodeID][key]
//   - {{nodes.nodeID.output_parsed.field}}    -> state["node_structured"][nodeID]["output_parsed"]["field"]
//
// For now, templates are best-effort: missing variables are rendered as
// empty strings.
func (c *HTTPRequestComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	method := strings.ToUpper(config.GetString("method"))
	if method == "" {
		method = http.MethodGet
	}

	urlTemplate := config.GetString("url_template")
	if urlTemplate == "" {
		return nil, fmt.Errorf("url_template is required for builtin.http_request")
	}

	urlStr := renderHTTPTemplate(urlTemplate, state)

	bodyTemplate := config.GetString("body_template")
	var bodyReader io.Reader
	if bodyTemplate != "" {
		bodyStr := renderHTTPTemplate(bodyTemplate, state)
		bodyReader = strings.NewReader(bodyStr)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Apply headers from config (if any)
	if rawHeaders := config.Get("headers"); rawHeaders != nil {
		switch h := rawHeaders.(type) {
		case map[string]any:
			for k, v := range h {
				headerValue := fmt.Sprint(v)
				headerValue = renderHTTPTemplate(headerValue, state)
				req.Header.Set(k, headerValue)
			}
		case map[string]string:
			for k, v := range h {
				headerValue := renderHTTPTemplate(v, state)
				req.Header.Set(k, headerValue)
			}
		default:
			// Ignore invalid headers type
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response body: %w", err)
	}

	// Convert headers to a simple map[string]any
	respHeaders := make(map[string]any)
	for k, values := range resp.Header {
		if len(values) == 1 {
			respHeaders[k] = values[0]
		} else {
			respHeaders[k] = values
		}
	}

	result := graph.State{
		"status_code":      resp.StatusCode,
		"response_body":    string(respBytes),
		"response_headers": respHeaders,
	}

	return result, nil
}

var httpTemplateVarPattern = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// renderHTTPTemplate performs simple template interpolation against graph.State.
func renderHTTPTemplate(tmpl string, state graph.State) string {
	if tmpl == "" {
		return ""
	}

	out := httpTemplateVarPattern.ReplaceAllStringFunc(tmpl, func(m string) string {
		matches := httpTemplateVarPattern.FindStringSubmatch(m)
		if len(matches) != 2 {
			return ""
		}
		expr := matches[1]

		// state.foo or state.foo.bar (nested)
		if strings.HasPrefix(expr, "state.") {
			key := strings.TrimPrefix(expr, "state.")
			parts := strings.Split(key, ".")
			if len(parts) == 0 {
				return ""
			}
			if val, ok := lookupNestedValue(map[string]any(state), parts); ok {
				return fmt.Sprint(val)
			}
			return ""
		}

		// nodes.nodeID.key or nodes.nodeID.key.subkey (nested)
		if strings.HasPrefix(expr, "nodes.") {
			parts := strings.Split(expr, ".")
			if len(parts) >= 3 {
				nodeID := parts[1]
				fieldPath := parts[2:]

				// Prefer structured per-node outputs when available.
				if raw, ok := state["node_structured"]; ok {
					if structuredMap, ok := raw.(map[string]any); ok {
						if nodeOut, ok := structuredMap[nodeID]; ok {
							if v, ok := lookupNestedValue(nodeOut, fieldPath); ok {
								return fmt.Sprint(v)
							}
						}
					}
				}

				// Fallback to textual node_responses for backwards compatibility.
				if raw, ok := state[graph.StateKeyNodeResponses]; ok {
					if nodeMap, ok := raw.(map[string]any); ok {
						if nodeOut, ok := nodeMap[nodeID]; ok {
							if v, ok := lookupNestedValue(nodeOut, fieldPath); ok {
								return fmt.Sprint(v)
							}
						}
					}
				}
			}
			return ""
		}

		// Fallback: unknown expression -> empty string
		return ""
	})

	return out
}

// lookupNestedValue resolves a path like ["output_parsed", "classification"]
// against a nested map structure. It is intentionally conservative and only
// supports map[string]any nesting, which is sufficient for structured_output
// style JSON objects.
func lookupNestedValue(root any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}

	current := root
	for _, part := range path {
		switch m := current.(type) {
		case map[string]any:
			val, ok := m[part]
			if !ok {
				return nil, false
			}
			current = val
		default:
			return nil, false
		}
	}

	return current, true
}
