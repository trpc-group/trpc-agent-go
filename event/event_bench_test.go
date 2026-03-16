//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"context"
	"runtime"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

func BenchmarkEmitEventWithTimeoutBufferedNoTimeout(b *testing.B) {
	log.SetTraceEnabled(false)
	b.Cleanup(func() {
		log.SetTraceEnabled(false)
	})
	ctx := context.Background()
	ch := make(chan *Event, 1)
	evt := New("benchmark-invocation", "benchmark-author")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := EmitEventWithTimeout(ctx, ch, evt, EmitWithoutTimeout); err != nil {
			b.Fatal(err)
		}
		<-ch
	}
}

func BenchmarkEmitEventWithTimeoutBufferedTimeout(b *testing.B) {
	log.SetTraceEnabled(false)
	b.Cleanup(func() {
		log.SetTraceEnabled(false)
	})
	ctx := context.Background()
	ch := make(chan *Event, 1)
	evt := New("benchmark-invocation", "benchmark-author")
	timeout := time.Second
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := EmitEventWithTimeout(ctx, ch, evt, timeout); err != nil {
			b.Fatal(err)
		}
		<-ch
	}
}

func BenchmarkEmitEventWithTimeoutBufferedFullNoTimeout(b *testing.B) {
	log.SetTraceEnabled(false)
	b.Cleanup(func() {
		log.SetTraceEnabled(false)
	})
	ctx := context.Background()
	ch := make(chan *Event, 1)
	errCh := make(chan error, 1)
	evt := New("benchmark-invocation", "benchmark-author")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch <- evt
		go func() {
			errCh <- EmitEventWithTimeout(ctx, ch, evt, EmitWithoutTimeout)
		}()
		runtime.Gosched()
		<-ch
		if err := <-errCh; err != nil {
			b.Fatal(err)
		}
		<-ch
	}
}

func BenchmarkEmitEventWithTimeoutBufferedFullTimeout(b *testing.B) {
	log.SetTraceEnabled(false)
	b.Cleanup(func() {
		log.SetTraceEnabled(false)
		log.SetLevel(log.LevelInfo)
	})
	log.SetLevel(log.LevelError)
	ctx := context.Background()
	ch := make(chan *Event, 1)
	evt := New("benchmark-invocation", "benchmark-author")
	timeout := time.Nanosecond
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch <- evt
		if err := EmitEventWithTimeout(ctx, ch, evt, timeout); err == nil {
			b.Fatal("expected timeout")
		}
		<-ch
	}
}
