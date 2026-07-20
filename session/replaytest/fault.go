//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// InjectFault returns a deep-copied snapshot with one deterministic regression.
func InjectFault(input Snapshot, kind FaultKind) (Snapshot, error) {
	output, err := cloneSnapshot(input)
	if err != nil {
		return Snapshot{}, err
	}
	switch kind {
	case FaultEventContent:
		message, err := firstEventMessage(output.Events)
		if err != nil {
			return Snapshot{}, err
		}
		message["content"] = "injected-content-drift"
	case FaultEventOrder:
		if len(output.Events) < 2 {
			return Snapshot{}, errors.New("event order fault requires two events")
		}
		output.Events[0], output.Events[1] = output.Events[1], output.Events[0]
	case FaultToolArguments:
		if err := mutateToolArguments(output.Events); err != nil {
			return Snapshot{}, err
		}
	case FaultStateValue:
		if err := mutateState(output.State); err != nil {
			return Snapshot{}, err
		}
	case FaultMemoryContent:
		if len(output.Memories) == 0 {
			return Snapshot{}, errors.New("memory fault requires a memory")
		}
		memoryValue, ok := output.Memories[0]["memory"].(map[string]any)
		if !ok {
			return Snapshot{}, errors.New("memory payload is missing")
		}
		memoryValue["memory"] = "injected-memory-drift"
	case FaultSummaryText:
		summary, err := firstSummary(output.Summaries)
		if err != nil {
			return Snapshot{}, err
		}
		summary["text"] = "injected-summary-drift"
	case FaultSummaryMissing:
		key, _, err := firstSummaryEntry(output.Summaries)
		if err != nil {
			return Snapshot{}, err
		}
		delete(output.Summaries, key)
	case FaultSummaryFilterKey:
		key, summary, err := firstSummaryEntry(output.Summaries)
		if err != nil {
			return Snapshot{}, err
		}
		delete(output.Summaries, key)
		output.Summaries["wrong/filter"] = summary
	case FaultTrackPayload:
		trackEvent, err := firstTrackEvent(output.Tracks)
		if err != nil {
			return Snapshot{}, err
		}
		payload, ok := trackEvent["payload"].(map[string]any)
		if !ok {
			return Snapshot{}, errors.New("track payload is missing")
		}
		payload["status"] = "injected-track-drift"
	case FaultDuplicateEvent:
		if len(output.Events) == 0 {
			return Snapshot{}, errors.New("duplicate fault requires an event")
		}
		output.Events = append(output.Events, cloneJSONMap(output.Events[0]))
	case FaultSummaryOwner:
		summary, err := firstSummary(output.Summaries)
		if err != nil {
			return Snapshot{}, err
		}
		summary["session_id"] = "wrong-session"
	default:
		return Snapshot{}, fmt.Errorf("unknown fault %q", kind)
	}
	output.Backend += "-faulted"
	return output, nil
}

func cloneSnapshot(input Snapshot) (Snapshot, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Snapshot{}, err
	}
	var output Snapshot
	if err := json.Unmarshal(raw, &output); err != nil {
		return Snapshot{}, err
	}
	return output, nil
}

func firstEventMessage(events []CanonicalMap) (map[string]any, error) {
	for _, evt := range events {
		choices, ok := evt["choices"].([]any)
		if !ok {
			continue
		}
		for _, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]any)
			if !ok {
				continue
			}
			message, ok := choice["message"].(map[string]any)
			if ok {
				return message, nil
			}
		}
	}
	return nil, errors.New("event content fault requires a message")
}

func mutateToolArguments(events []CanonicalMap) error {
	for _, evt := range events {
		choices, _ := evt["choices"].([]any)
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]any)
			message, _ := choice["message"].(map[string]any)
			calls, _ := message["tool_calls"].([]any)
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, ok := call["function"].(map[string]any)
				if !ok {
					continue
				}
				function["arguments"] = `{"city":"wrong"}`
				return nil
			}
		}
	}
	return errors.New("tool argument fault requires a tool call")
}

func mutateState(state map[string]map[string]string) error {
	for _, scope := range []string{"session", "user", "app"} {
		values := state[scope]
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			values[keys[0]] = `"injected-state-drift"`
			return nil
		}
	}
	return errors.New("state fault requires a state value")
}

func firstSummary(summaries map[string]CanonicalMap) (CanonicalMap, error) {
	_, summary, err := firstSummaryEntry(summaries)
	return summary, err
}

func firstSummaryEntry(summaries map[string]CanonicalMap) (string, CanonicalMap, error) {
	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", nil, errors.New("summary fault requires a summary")
	}
	return keys[0], summaries[keys[0]], nil
}

func firstTrackEvent(tracks map[string][]CanonicalMap) (CanonicalMap, error) {
	keys := make([]string, 0, len(tracks))
	for key := range tracks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(tracks[key]) > 0 {
			return tracks[key][0], nil
		}
	}
	return nil, errors.New("track fault requires a track event")
}
