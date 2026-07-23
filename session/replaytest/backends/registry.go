//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package backends wires session and memory service implementations into a
// uniform Backend value the replay harness can drive. It intentionally depends
// only on the public service interfaces plus session/summary; it must NOT
// import the harness package (that would create an import cycle, since the
// harness runner imports backends).
package backends

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Backend bundles a session and memory service under one named backend, along
// with capability flags used by the allowed-diff classifier.
type Backend struct {
	Name              string
	Session           session.Service
	Memory            memory.Service
	SupportsEventPage bool
	SupportsTTL       bool
	cleanup           func()
}

// Close releases the backend's session, memory, and any temp resources.
func (b *Backend) Close() error {
	var errs []error
	if b.Session != nil {
		if err := b.Session.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if b.Memory != nil {
		if err := b.Memory.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if b.cleanup != nil {
		b.cleanup()
	}
	return errors.Join(errs...)
}

// EnabledBackends returns the backends to compare for the current run. The
// baseline is always index 0 (inmemory), followed by sqlite, followed by any
// env-gated external backends. All session services are wired with the given
// summarizer so summary operations behave deterministically.
func EnabledBackends(summarizer summary.SessionSummarizer) ([]*Backend, error) {
	inmem, err := newInMemoryBackend(summarizer)
	if err != nil {
		return nil, err
	}
	sqlite, err := newSQLiteBackend(summarizer)
	if err != nil {
		_ = inmem.Close()
		return nil, err
	}
	bs := []*Backend{inmem, sqlite}
	bs = append(bs, externalBackends(summarizer)...)
	return bs, nil
}
