// Package model provides interfaces for working with LLMs.
package model

import "context"

// Model is the interface for all language models.
type Model interface {
	GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)
}
