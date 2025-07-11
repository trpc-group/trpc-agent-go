package query

import "context"

// PassthroughEnhancer is a simple enhancer that returns the original query unchanged.
type PassthroughEnhancer struct {
	// Configuration options can be added here in the future.
}

// Option represents a functional option for configuring PassthroughEnhancer.
type Option func(*PassthroughEnhancer)

// NewPassthroughEnhancer creates a new passthrough query enhancer with options.
func NewPassthroughEnhancer(opts ...Option) *PassthroughEnhancer {
	pe := &PassthroughEnhancer{}

	// Apply options.
	for _, opt := range opts {
		opt(pe)
	}

	return pe
}

// EnhanceQuery implements the Enhancer interface by returning the original query.
func (p *PassthroughEnhancer) EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error) {
	return &Enhanced{
		Enhanced: req.Query,
		Keywords: []string{req.Query},
	}, nil
}
