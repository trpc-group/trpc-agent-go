// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func faultDropLastEvent(snap *Snapshot) error {
	if snap.Session == nil || len(snap.Session.Events) == 0 {
		return fmt.Errorf("no events to drop")
	}
	snap.Session.Events = snap.Session.Events[:len(snap.Session.Events)-1]
	return nil
}

func faultMutateLastContent(snap *Snapshot) error {
	if snap.Session == nil || len(snap.Session.Events) == 0 {
		return fmt.Errorf("no events to mutate")
	}
	i := len(snap.Session.Events) - 1
	e := snap.Session.Events[i]
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return fmt.Errorf("no content to mutate")
	}
	rsp := *e.Response
	rsp.Choices = append([]model.Choice(nil), e.Response.Choices...)
	msg := rsp.Choices[0].Message
	msg.Content = msg.Content + "|fault"
	rsp.Choices[0].Message = msg
	e.Response = &rsp
	snap.Session.Events[i] = e
	return nil
}

func faultDropSummary(snap *Snapshot) error {
	if snap.Session == nil {
		return fmt.Errorf("no session")
	}
	if len(snap.Session.Summaries) == 0 {
		return fmt.Errorf("no summaries to drop")
	}
	snap.Session.Summaries = map[string]*session.Summary{}
	return nil
}

func faultOverwriteSummary(snap *Snapshot) error {
	if snap.Session == nil {
		return fmt.Errorf("no session")
	}
	if snap.Session.Summaries == nil {
		snap.Session.Summaries = map[string]*session.Summary{}
	}
	if sum, ok := snap.Session.Summaries[""]; ok && sum != nil {
		cp := *sum
		cp.Summary = sum.Summary + "|overwrite"
		snap.Session.Summaries[""] = &cp
		return nil
	}
	snap.Session.Summaries[""] = &session.Summary{Summary: "fault-overwrite"}
	return nil
}

func faultWrongSummaryFilterKey(snap *Snapshot) error {
	if snap.Session == nil || snap.Session.Summaries == nil {
		return fmt.Errorf("no summaries")
	}
	var sum *session.Summary
	if v, ok := snap.Session.Summaries[""]; ok {
		sum = v
		delete(snap.Session.Summaries, "")
	} else {
		keys := make([]string, 0, len(snap.Session.Summaries))
		for k := range snap.Session.Summaries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return fmt.Errorf("no summary to rekey")
		}
		k := keys[0]
		sum = snap.Session.Summaries[k]
		delete(snap.Session.Summaries, k)
	}
	if sum == nil {
		return fmt.Errorf("no summary to rekey")
	}
	snap.Session.Summaries["wrong-filter-key"] = sum
	return nil
}

func faultMutateState(snap *Snapshot) error {
	if snap.Session == nil {
		return fmt.Errorf("no session")
	}
	if snap.Session.State == nil {
		snap.Session.State = session.StateMap{}
	}
	snap.Session.State["color"] = []byte("fault-color")
	return nil
}

func faultDropTrack(snap *Snapshot) error {
	if snap.Session == nil {
		return fmt.Errorf("no session")
	}
	if len(snap.Session.Tracks) == 0 {
		return fmt.Errorf("no tracks to drop")
	}
	snap.Session.Tracks = map[session.Track]*session.TrackEvents{}
	return nil
}

func faultMutateMemoryContent(snap *Snapshot) error {
	if len(snap.Memories) == 0 || snap.Memories[0] == nil || snap.Memories[0].Memory == nil {
		return fmt.Errorf("no memory")
	}
	cp := *snap.Memories[0]
	m := *snap.Memories[0].Memory
	m.Memory = m.Memory + "|fault"
	cp.Memory = &m
	snap.Memories[0] = &cp
	return nil
}

func faultDropMemory(snap *Snapshot) error {
	if len(snap.Memories) == 0 {
		return fmt.Errorf("no memories to drop")
	}
	snap.Memories = nil
	return nil
}

func faultReorderEvents(snap *Snapshot) error {
	if snap.Session == nil || len(snap.Session.Events) < 2 {
		return fmt.Errorf("need >=2 events")
	}
	snap.Session.Events[0], snap.Session.Events[1] = snap.Session.Events[1], snap.Session.Events[0]
	return nil
}

func faultDuplicateEvent(snap *Snapshot) error {
	if snap.Session == nil || len(snap.Session.Events) == 0 {
		return fmt.Errorf("no events")
	}
	last := snap.Session.Events[len(snap.Session.Events)-1]
	snap.Session.Events = append(snap.Session.Events, last)
	return nil
}
