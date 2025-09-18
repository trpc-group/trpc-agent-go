package sse

import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

// Option is a function that configures a Service.
type Option func(*Service)

// WithAddress sets the listening address.
func WithAddress(addr string) Option {
	return func(s *Service) {
		s.addr = addr
	}
}

// WithPath sets the request path.
func WithPath(path string) Option {
	return func(s *Service) {
		s.path = path
	}
}

// WithRunner sets the runner responsible for executing requests.
func WithRunner(r runner.Runner) Option {
	return func(s *Service) {
		if r != nil {
			s.runner = r
		}
	}
}
