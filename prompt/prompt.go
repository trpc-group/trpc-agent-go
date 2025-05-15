// Package prompt provides a structured system for managing, versioning, and reusing
// prompt templates for language models.
package prompt

import (
	"context"
)

// Template represents a structured prompt template with optional variable placeholders.
type Template struct {
	// ID is a unique identifier for the template.
	ID string `json:"id"`

	// Name is a human-readable name for the template.
	Name string `json:"name"`

	// Description provides details about the template's purpose and usage.
	Description string `json:"description"`

	// Version tracks the template version for compatibility and change management.
	Version string `json:"version"`

	// Content contains the actual template text with optional placeholders.
	Content string `json:"content"`

	// Variables holds metadata about the variables used in the template.
	Variables []Variable `json:"variables,omitempty"`

	// Tags are optional labels for categorizing and filtering templates.
	Tags []string `json:"tags,omitempty"`

	// ModelCompatibility indicates which models this template is optimized for.
	ModelCompatibility []string `json:"model_compatibility,omitempty"`
}

// Variable represents a placeholder in a template that can be replaced with actual values.
type Variable struct {
	// Name is the identifier for the variable in the template.
	Name string `json:"name"`

	// Description explains what the variable represents.
	Description string `json:"description"`

	// Required indicates if the variable must be provided.
	Required bool `json:"required"`

	// DefaultValue is used when the variable is not explicitly provided.
	DefaultValue string `json:"default_value,omitempty"`
}

// Repository provides storage and retrieval of prompt templates.
type Repository interface {
	// Get retrieves a template by its ID.
	Get(ctx context.Context, id string) (*Template, error)

	// List returns templates matching the specified filter criteria.
	List(ctx context.Context, filter Filter) ([]*Template, error)

	// Save persists a template to storage.
	Save(ctx context.Context, template *Template) error

	// Delete removes a template from storage.
	Delete(ctx context.Context, id string) error
}

// Filter defines criteria for searching and filtering templates.
type Filter struct {
	// Tags to filter by.
	Tags []string

	// ModelNames to filter by model compatibility.
	ModelNames []string

	// NameContains filters templates whose names contain this substring.
	NameContains string

	// VersionExact matches templates with the exact version.
	VersionExact string
}

// Renderer processes a template by replacing variables with actual values.
type Renderer interface {
	// Render processes the template and returns the final prompt string.
	Render(ctx context.Context, template *Template, variables map[string]string) (string, error)
}

// Manager combines repository access and template rendering.
type Manager interface {
	Repository

	// Render processes a template by ID and returns the rendered content.
	Render(ctx context.Context, id string, variables map[string]string) (string, error)

	// GetRenderer returns the renderer used by this manager.
	GetRenderer() Renderer
}

// Common errors returned by the prompt package.
var (
	ErrTemplateNotFound   = PromptError{Code: "template_not_found", Message: "template not found"}
	ErrMissingRequiredVar = PromptError{Code: "missing_required_variable", Message: "missing required variable"}
	ErrInvalidTemplate    = PromptError{Code: "invalid_template", Message: "invalid template format"}
	ErrTemplateExists     = PromptError{Code: "template_exists", Message: "template already exists"}
	ErrRepositoryError    = PromptError{Code: "repository_error", Message: "repository operation failed"}
	ErrRenderingError     = PromptError{Code: "rendering_error", Message: "error rendering template"}
)

// PromptError represents errors in the prompt system.
type PromptError struct {
	Code    string
	Message string
	Cause   error
}

// Error implements the error interface.
func (e PromptError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

// WithCause attaches an underlying cause to the error.
func (e PromptError) WithCause(cause error) PromptError {
	e.Cause = cause
	return e
}
