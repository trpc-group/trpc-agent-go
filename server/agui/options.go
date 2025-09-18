package agui

import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Option customizes server behaviour.
type Option func(*options)

type options struct {
	service        service.Service
	sessionService session.Service
}

// WithService replaces the default transport service.
func WithService(s service.Service) Option {
	return func(o *options) {
		o.service = s
	}
}

// WithSessionService replaces the default session service implementation.
func WithSessionService(svc session.Service) Option {
	return func(o *options) {
		o.sessionService = svc
	}
}
