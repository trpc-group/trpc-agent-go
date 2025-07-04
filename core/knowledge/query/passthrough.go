package query

import "context"

// PassthroughEnhancer is a simple enhancer that returns the original query unchanged.
type PassthroughEnhancer struct{}

// NewPassthroughEnhancer creates a new passthrough query enhancer.
func NewPassthroughEnhancer() *PassthroughEnhancer {
	return &PassthroughEnhancer{}
}

// EnhanceQuery implements the Enhancer interface by returning the original query.
func (p *PassthroughEnhancer) EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error) {
	return &Enhanced{
		Enhanced: req.Query,
		Keywords: []string{req.Query},
	}, nil
}
