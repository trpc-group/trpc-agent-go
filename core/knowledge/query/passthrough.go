package query

import "context"

// PassthroughEnhancer is a simple enhancer that returns the original query unchanged.
type PassthroughEnhancer struct{}

// NewPassthroughEnhancer creates a new passthrough query enhancer.
func NewPassthroughEnhancer() *PassthroughEnhancer {
	return &PassthroughEnhancer{}
}

// EnhanceQuery implements the Enhancer interface by returning the original query.
func (p *PassthroughEnhancer) EnhanceQuery(ctx context.Context, query string) (*Enhanced, error) {
	return &Enhanced{
		Original: query,
		Enhanced: query,
		Keywords: []string{query},
	}, nil
}
