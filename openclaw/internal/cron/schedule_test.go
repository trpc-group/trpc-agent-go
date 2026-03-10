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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduleHelpers(t *testing.T) {
	now := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)

	atTime, err := parseAtTime("2026-03-06T10:05:00Z")
	require.NoError(t, err)
	require.Equal(
		t,
		time.Date(2026, 3, 6, 10, 5, 0, 0, time.UTC),
		atTime,
	)

	interval, err := parseEveryInterval(Schedule{EveryMS: 1_500})
	require.NoError(t, err)
	require.Equal(t, 1500*time.Millisecond, interval)

	interval, err = parseEveryInterval(Schedule{Every: "2m"})
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, interval)

	spec, err := parseCronSpec(Schedule{CronExpr: "*/2 * * * *"})
	require.NoError(t, err)
	require.Equal(t, now.Add(2*time.Minute), spec.Next(now))

	_, err = parseCronSpec(Schedule{CronExpr: "* * *"})
	require.Error(t, err)
}

func TestComputeNextRunVariants(t *testing.T) {
	now := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)

	next, err := computeNextRun(Schedule{
		Kind:  ScheduleKindEvery,
		Every: "1m",
	}, now)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Minute), next)

	next, err = computeNextRun(Schedule{
		Kind:     ScheduleKindCron,
		CronExpr: "*/5 * * * *",
	}, now)
	require.NoError(t, err)
	require.Equal(t, now.Add(5*time.Minute), next)

	_, err = computeNextRun(Schedule{Kind: ""}, now)
	require.Error(t, err)

	nextPtr, err := computeNextAfterRun(Schedule{
		Kind: ScheduleKindAt,
		At:   "2026-03-06T10:05:00Z",
	}, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.Nil(t, nextPtr)

	nextPtr, err = computeNextAfterRun(Schedule{
		Kind:  ScheduleKindEvery,
		Every: "1m",
	}, now, now.Add(3*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, nextPtr)
	require.Equal(t, now.Add(4*time.Minute), *nextPtr)

	nextPtr, err = computeNextAfterRun(Schedule{
		Kind:     ScheduleKindCron,
		CronExpr: "*/2 * * * *",
	}, now, now.Add(5*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, nextPtr)
	require.Equal(t, now.Add(6*time.Minute), *nextPtr)
}

func TestScheduleHelpers_ErrorCases(t *testing.T) {
	t.Parallel()

	_, err := computeInitialNextRun(
		Schedule{Kind: ScheduleKindEvery, Every: "2m"},
		time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	_, err = parseAtTime(" ")
	require.Error(t, err)

	_, err = parseEveryInterval(Schedule{})
	require.Error(t, err)

	_, err = parseEveryInterval(Schedule{Every: "-1m"})
	require.Error(t, err)

	_, err = parseEveryInterval(Schedule{Every: "bad"})
	require.Error(t, err)

	_, err = parseCronSpec(Schedule{})
	require.Error(t, err)

	_, err = parseCronSpec(Schedule{
		CronExpr: "*/5 * * * *",
		Timezone: "Bad/Timezone",
	})
	require.Error(t, err)

	_, err = parseCronSpec(Schedule{CronExpr: "* * *"})
	require.Error(t, err)

	_, err = computeNextAfterRun(
		Schedule{Kind: "weird"},
		time.Now(),
		time.Now(),
	)
	require.Error(t, err)
}

func TestParseCronSpec_WithTimezone(t *testing.T) {
	t.Parallel()

	spec, err := parseCronSpec(Schedule{
		CronExpr: "0 */5 * * * *",
		Timezone: "Asia/Shanghai",
	})
	require.NoError(t, err)

	base := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	next := spec.Next(base)
	require.True(t, next.After(base))
}
