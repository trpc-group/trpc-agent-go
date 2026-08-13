//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"errors"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type Call struct {
	Responses []*model.Response
}

// QueueModel is a synchronized FIFO of model calls. Each GenerateContent call
// consumes one Call previously added with Push.
type QueueModel struct {
	mu    sync.Mutex
	Calls []Call
}

// Push adds a Call to the end of the queue. It is safe to call concurrently
// with Push or GenerateContent.
func (m *QueueModel) Push(call Call) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, call)
}

// GenerateContent consumes and returns the next queued Call. A nil request is
// rejected without consuming a Call, and an empty queue returns an error.
func (m *QueueModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("mock model: request is nil")
	}
	m.mu.Lock()
	if len(m.Calls) == 0 {
		m.mu.Unlock()
		return nil, errors.New("mock model: no queued calls")
	}
	call := m.Calls[0]
	m.Calls[0] = Call{}
	m.Calls = m.Calls[1:]
	m.mu.Unlock()

	ch := make(chan *model.Response)
	go func() {
		defer close(ch)
		for _, resp := range call.Responses {
			select {
			case <-ctx.Done():
				return
			case ch <- resp:
			}
		}
	}()
	return ch, nil
}

func (m *QueueModel) Info() model.Info {
	return model.Info{Name: "mock-model"}
}
