//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
)

// RunFaulty replays a case normally, then injects the declared fault into the
// read-back projection. Fault injection happens after backend writes complete,
// so it cannot break write atomicity while still exercising the diff pipeline.
func RunFaulty(ctx context.Context, b *backends.Backend, c *ReplayCase) (*Snapshot, error) {
	snap, err := Run(ctx, b, c)
	if err != nil {
		return nil, err
	}
	injectFault(snap, c.FaultInjection)
	return snap, nil
}

func injectFault(s *Snapshot, fault string) {
	switch fault {
	case "duplicate_event":
		if len(s.Events) > 0 {
			s.Events = append([]EventView{s.Events[0]}, s.Events...)
		}
	case "tamper_state":
		tamperState(s)
	case "drop_memory":
		if len(s.Memories) > 0 {
			s.Memories = s.Memories[1:]
		} else {
			s.Memories = append(s.Memories, MemoryView{ID: "fault", Content: "unexpected memory"})
		}
	case "drop_summary":
		if len(s.Summaries) > 0 {
			s.Summaries = s.Summaries[1:]
		} else {
			s.Summaries = append(s.Summaries, SummaryView{FilterKey: "fault", Text: "unexpected summary"})
		}
	case "overwrite_summary":
		if len(s.Summaries) > 0 {
			s.Summaries[0].Text = "fault: overwritten summary"
		} else {
			s.Summaries = append(s.Summaries, SummaryView{Text: "fault: overwritten summary"})
		}
	case "wrong_summary_session":
		if len(s.Summaries) > 0 {
			s.Summaries[0].SessionID = "wrong-session"
		} else {
			s.Summaries = append(s.Summaries, SummaryView{Text: "fault", SessionID: "wrong-session"})
		}
	case "wrong_filterkey":
		if len(s.Summaries) > 0 {
			s.Summaries[0].FilterKey = "wrong-filter-key"
		} else {
			s.Summaries = append(s.Summaries, SummaryView{FilterKey: "wrong-filter-key", Text: "fault"})
		}
	case "wrong_track_payload":
		if len(s.Tracks) > 0 {
			s.Tracks[0].Payload = map[string]any{"fault": "wrong payload"}
		} else {
			s.Tracks = append(s.Tracks, TrackView{Name: "tool_exec", Payload: map[string]any{"fault": "wrong payload"}})
		}
	}
}

func tamperState(s *Snapshot) {
	if s.State == nil {
		s.State = map[string]string{}
	}
	for k := range s.State {
		s.State[k] = "fault: tampered state"
		return
	}
	s.State["fault"] = "tampered state"
}
