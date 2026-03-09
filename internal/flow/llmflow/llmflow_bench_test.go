//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	// benchCountSink and benchRespSink prevent the compiler from optimizing away the benchmarked work.
	benchRespSink  *model.Response
	benchCountSink int
)

type benchChanModel struct {
	responses []*model.Response
}

func (m *benchChanModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	go func() {
		for _, resp := range m.responses {
			ch <- resp
		}
		close(ch)
	}()
	return ch, nil
}

func (m *benchChanModel) Info() model.Info {
	return model.Info{Name: "benchChanModel"}
}

type benchIterModel struct {
	responses []*model.Response
}

func (m *benchIterModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *benchIterModel) GenerateContentIter(ctx context.Context, request *model.Request) (model.Seq[*model.Response], error) {
	return func(yield func(*model.Response) bool) {
		for _, resp := range m.responses {
			if !yield(resp) {
				return
			}
		}
	}, nil
}

func (m *benchIterModel) Info() model.Info {
	return model.Info{Name: "benchIterModel"}
}

func makeBenchResponses(n int) []*model.Response {
	responses := make([]*model.Response, n)
	for i := 0; i < n; i++ {
		responses[i] = &model.Response{Created: int64(i + 1)}
	}
	return responses
}

func consumeSeq(seq model.Seq[*model.Response]) (checksum int, last *model.Response) {
	seq(func(resp *model.Response) bool {
		checksum += int(resp.Created)
		last = resp
		return true
	})
	return checksum, last
}

func BenchmarkGenerateContentSeq(b *testing.B) {
	ctx := context.Background()
	f := new(Flow)
	invocation := &agent.Invocation{AgentName: "bench"}
	request := &model.Request{}

	for _, n := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprintf("Channel/n=%d", n), func(b *testing.B) {
			invocation.Model = &benchChanModel{responses: makeBenchResponses(n)}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seq, err := f.generateContentSeq(ctx, invocation, request)
				if err != nil {
					b.Fatal(err)
				}
				benchCountSink, benchRespSink = consumeSeq(seq)
			}
		})

		b.Run(fmt.Sprintf("Iter/n=%d", n), func(b *testing.B) {
			invocation.Model = &benchIterModel{responses: makeBenchResponses(n)}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seq, err := f.generateContentSeq(ctx, invocation, request)
				if err != nil {
					b.Fatal(err)
				}
				benchCountSink, benchRespSink = consumeSeq(seq)
			}
		})
	}
}
