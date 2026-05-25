//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type ingestJob struct {
	req    captureRequest
	sess   *session.Session
	cursor time.Time
}

func (s *Service) startWorkers() {
	for i := 0; i < s.opts.IngestWorkers; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for job := range s.queue {
				ctx := context.Background()
				if s.opts.IngestJobTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, s.opts.IngestJobTimeout)
					if err := s.capture(ctx, job); err != nil {
						log.Warnf("tencentdb memory: async capture failed: %v", err)
					}
					cancel()
					continue
				}
				if err := s.capture(ctx, job); err != nil {
					log.Warnf("tencentdb memory: async capture failed: %v", err)
				}
			}
		}()
	}
}
