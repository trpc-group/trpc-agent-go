//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
)

// progressPrinter renders a multi-line progress display for knowledge loading.
// Each source gets its own progress bar; the display is refreshed in-place
// using ANSI escape codes.
type progressPrinter struct {
	mu      sync.Mutex
	sources []string
	state   map[string]*srcState
	printed int
}

type srcState struct {
	processed int
	total     int
	err       error
}

func newProgressPrinter() *progressPrinter {
	return &progressPrinter{state: make(map[string]*srcState)}
}

func (p *progressPrinter) onProgress(_ context.Context, evt knowledge.LoadProgressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.sources == nil {
		p.sources = evt.SourceNames
	}

	if evt.Done {
		p.printDoneSummary(evt)
		return
	}

	if evt.Err != nil {
		if evt.SourceName != "" {
			st := p.getOrCreate(evt.SourceName)
			st.err = evt.Err
		}
		p.redraw(evt)
		return
	}

	if evt.SourceName != "" {
		st := p.getOrCreate(evt.SourceName)
		st.processed = evt.SourceProcessed
		st.total = evt.SourceTotal
	}
	p.redraw(evt)
}

func (p *progressPrinter) getOrCreate(name string) *srcState {
	st, ok := p.state[name]
	if !ok {
		st = &srcState{}
		p.state[name] = st
	}
	return st
}

func (p *progressPrinter) redraw(evt knowledge.LoadProgressEvent) {
	if p.printed > 0 {
		fmt.Printf("\033[%dA", p.printed)
	}

	const barWidth = 20
	lines := 0
	for _, name := range p.sources {
		st := p.state[name]

		pct := 0
		if st != nil && st.total > 0 {
			pct = st.processed * 100 / st.total
		}
		filled := pct * barWidth / 100
		bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)

		suffix := "waiting..."
		if st != nil {
			if st.err != nil {
				suffix = fmt.Sprintf("%d/%d  FAILED: %v", st.processed, st.total, st.err)
			} else if st.processed >= st.total && st.total > 0 {
				suffix = fmt.Sprintf("%d/%d  done", st.processed, st.total)
			} else {
				suffix = fmt.Sprintf("%d/%d", st.processed, st.total)
			}
		}
		fmt.Printf("\033[2K  %-18s [%s] %3d%%  %s\n", truncate(name, 18), bar, pct, suffix)
		lines++
	}

	fmt.Printf("\033[2K  total: %d docs  elapsed: %s\n",
		evt.Total, evt.TotalElapsed.Truncate(time.Millisecond))
	lines++

	p.printed = lines
}

func (p *progressPrinter) printDoneSummary(evt knowledge.LoadProgressEvent) {
	failed := 0
	for _, st := range p.state {
		if st != nil && st.err != nil {
			failed++
		}
	}

	if failed > 0 {
		fmt.Printf("  Load finished with %d failed source(s). total: %d docs  elapsed: %s\n",
			failed, evt.Total, evt.TotalElapsed.Truncate(time.Millisecond))
		return
	}
	fmt.Printf("  All sources loaded successfully. total: %d docs  elapsed: %s\n",
		evt.Total, evt.TotalElapsed.Truncate(time.Millisecond))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
