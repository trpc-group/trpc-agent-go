package prompt

import (
	"context"
	"regexp"
	"strings"
)

// SimpleRenderer implements the Renderer interface with basic variable substitution.
type SimpleRenderer struct {
	pattern *regexp.Regexp
}

// NewSimpleRenderer creates a new SimpleRenderer.
func NewSimpleRenderer() *SimpleRenderer {
	return &SimpleRenderer{
		pattern: regexp.MustCompile(`\{\{([^{}]+)\}\}`),
	}
}

// Render processes a template by replacing variables with values.
func (r *SimpleRenderer) Render(_ context.Context, template *Template, variables map[string]string) (string, error) {
	if template == nil {
		return "", ErrInvalidTemplate
	}

	// Replace all variables in the content
	result := r.pattern.ReplaceAllStringFunc(template.Content, func(match string) string {
		// Extract variable name
		varName := r.pattern.FindStringSubmatch(match)[1]
		varName = strings.TrimSpace(varName)

		// Replace with value or keep original
		if val, ok := variables[varName]; ok {
			return val
		}

		// Look for default value in template variables
		for _, v := range template.Variables {
			if v.Name == varName && v.DefaultValue != "" {
				return v.DefaultValue
			}
		}

		return match
	})

	return result, nil
}
