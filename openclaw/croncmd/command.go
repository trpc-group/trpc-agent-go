//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package croncmd

import (
	"errors"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
)

const (
	ActionHelp   = "help"
	ActionList   = "list"
	ActionStatus = "status"
	ActionStop   = "stop"
	ActionResume = "resume"
	ActionRemove = "remove"
	ActionClear  = "clear"
)

const shortJobIDSize = 8

var (
	ErrUnknownAction = errors.New("cron command: unknown action")
	ErrSelectorEmpty = errors.New("cron command: selector required")
	ErrSelectorMiss  = errors.New("cron command: job not found")
	ErrSelectorMany  = errors.New("cron command: job selector is ambiguous")
)

type Command struct {
	Action   string
	Selector string
}

func Parse(raw string) (Command, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Command{Action: ActionList}, nil
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return Command{Action: ActionList}, nil
	}

	action := strings.ToLower(strings.TrimSpace(fields[0]))
	if !isKnownAction(action) {
		return Command{}, ErrUnknownAction
	}

	return Command{
		Action:   action,
		Selector: strings.TrimSpace(trimmed[len(fields[0]):]),
	}, nil
}

func NeedsSelector(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case ActionStatus, ActionStop, ActionResume, ActionRemove:
		return true
	default:
		return false
	}
}

func ResolveSelector(
	jobs []gwclient.ScheduledJobSummary,
	selector string,
) (gwclient.ScheduledJobSummary, error) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" {
		return gwclient.ScheduledJobSummary{}, ErrSelectorEmpty
	}

	if index, ok := parseIndex(trimmed, len(jobs)); ok {
		return jobs[index], nil
	}

	if job, ok := exactIDMatch(jobs, trimmed); ok {
		return job, nil
	}

	if job, ok, err := uniqueIDPrefixMatch(jobs, trimmed); err != nil {
		return gwclient.ScheduledJobSummary{}, err
	} else if ok {
		return job, nil
	}

	if job, ok, err := uniqueNameMatch(jobs, trimmed); err != nil {
		return gwclient.ScheduledJobSummary{}, err
	} else if ok {
		return job, nil
	}

	return gwclient.ScheduledJobSummary{}, ErrSelectorMiss
}

func ShortID(jobID string) string {
	trimmed := strings.TrimSpace(jobID)
	if len(trimmed) <= shortJobIDSize {
		return trimmed
	}
	return trimmed[:shortJobIDSize]
}

func isKnownAction(action string) bool {
	switch action {
	case ActionHelp,
		ActionList,
		ActionStatus,
		ActionStop,
		ActionResume,
		ActionRemove,
		ActionClear:
		return true
	default:
		return false
	}
}

func parseIndex(raw string, size int) (int, bool) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > size {
		return 0, false
	}
	return value - 1, true
}

func exactIDMatch(
	jobs []gwclient.ScheduledJobSummary,
	selector string,
) (gwclient.ScheduledJobSummary, bool) {
	for _, job := range jobs {
		if strings.TrimSpace(job.ID) == selector {
			return job, true
		}
	}
	return gwclient.ScheduledJobSummary{}, false
}

func uniqueIDPrefixMatch(
	jobs []gwclient.ScheduledJobSummary,
	selector string,
) (gwclient.ScheduledJobSummary, bool, error) {
	var matched *gwclient.ScheduledJobSummary
	for i := range jobs {
		jobID := strings.TrimSpace(jobs[i].ID)
		if !strings.HasPrefix(jobID, selector) {
			continue
		}
		if matched != nil {
			return gwclient.ScheduledJobSummary{}, false, ErrSelectorMany
		}
		matched = &jobs[i]
	}
	if matched == nil {
		return gwclient.ScheduledJobSummary{}, false, nil
	}
	return *matched, true, nil
}

func uniqueNameMatch(
	jobs []gwclient.ScheduledJobSummary,
	selector string,
) (gwclient.ScheduledJobSummary, bool, error) {
	var matched *gwclient.ScheduledJobSummary
	for i := range jobs {
		name := strings.TrimSpace(jobs[i].Name)
		if !strings.EqualFold(name, selector) {
			continue
		}
		if matched != nil {
			return gwclient.ScheduledJobSummary{}, false, ErrSelectorMany
		}
		matched = &jobs[i]
	}
	if matched == nil {
		return gwclient.ScheduledJobSummary{}, false, nil
	}
	return *matched, true, nil
}
