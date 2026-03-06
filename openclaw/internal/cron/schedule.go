//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

import (
	"fmt"
	"strings"
	"time"

	robcron "github.com/robfig/cron/v3"
)

const (
	cronFieldsWithMinutes = 5
	cronFieldsWithSeconds = 6
)

func computeInitialNextRun(
	schedule Schedule,
	now time.Time,
) (*time.Time, error) {
	next, err := computeNextRun(schedule, now)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

func computeNextAfterRun(
	schedule Schedule,
	base time.Time,
	now time.Time,
) (*time.Time, error) {
	kind := strings.ToLower(strings.TrimSpace(schedule.Kind))
	switch kind {
	case ScheduleKindAt:
		return nil, nil
	case ScheduleKindEvery:
		interval, err := parseEveryInterval(schedule)
		if err != nil {
			return nil, err
		}
		next := base.Add(interval)
		if next.After(now) {
			return &next, nil
		}
		missed := (now.Sub(next) / interval) + 1
		next = next.Add(missed * interval)
		return &next, nil
	case ScheduleKindCron:
		spec, err := parseCronSpec(schedule)
		if err != nil {
			return nil, err
		}
		next := spec.Next(base)
		for !next.After(now) {
			next = spec.Next(next)
		}
		return &next, nil
	default:
		return nil, fmt.Errorf(
			"cron: unsupported schedule kind: %s",
			schedule.Kind,
		)
	}
}

func computeNextRun(
	schedule Schedule,
	now time.Time,
) (time.Time, error) {
	switch strings.ToLower(strings.TrimSpace(schedule.Kind)) {
	case ScheduleKindAt:
		return parseAtTime(schedule.At)
	case ScheduleKindEvery:
		interval, err := parseEveryInterval(schedule)
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(interval), nil
	case ScheduleKindCron:
		spec, err := parseCronSpec(schedule)
		if err != nil {
			return time.Time{}, err
		}
		return spec.Next(now), nil
	default:
		return time.Time{}, fmt.Errorf(
			"cron: unsupported schedule kind: %s",
			schedule.Kind,
		)
	}
}

func parseAtTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("cron: at schedule is required")
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"cron: invalid at schedule: %w",
			err,
		)
	}
	return t, nil
}

func parseEveryInterval(schedule Schedule) (time.Duration, error) {
	if schedule.EveryMS > 0 {
		return time.Duration(schedule.EveryMS) * time.Millisecond, nil
	}
	value := strings.TrimSpace(schedule.Every)
	if value == "" {
		return 0, fmt.Errorf("cron: every interval is required")
	}
	interval, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("cron: invalid every interval: %w", err)
	}
	if interval <= 0 {
		return 0, fmt.Errorf("cron: every interval must be positive")
	}
	return interval, nil
}

func parseCronSpec(schedule Schedule) (robcron.Schedule, error) {
	expr := strings.TrimSpace(schedule.CronExpr)
	if expr == "" {
		return nil, fmt.Errorf("cron: cron expression is required")
	}

	parser := robcron.NewParser(
		robcron.SecondOptional |
			robcron.Minute |
			robcron.Hour |
			robcron.Dom |
			robcron.Month |
			robcron.Dow |
			robcron.Descriptor,
	)

	loc := time.Local
	if rawTZ := strings.TrimSpace(schedule.Timezone); rawTZ != "" {
		loaded, err := time.LoadLocation(rawTZ)
		if err != nil {
			return nil, fmt.Errorf(
				"cron: invalid timezone: %w",
				err,
			)
		}
		loc = loaded
	}

	spec := expr
	fields := len(strings.Fields(expr))
	switch fields {
	case cronFieldsWithMinutes, cronFieldsWithSeconds:
	default:
		return nil, fmt.Errorf(
			"cron: expected 5 or 6 cron fields, got %d",
			fields,
		)
	}

	if loc != time.Local {
		spec = "CRON_TZ=" + loc.String() + " " + spec
	}
	scheduleValue, err := parser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("cron: invalid cron expression: %w", err)
	}
	return scheduleValue, nil
}
