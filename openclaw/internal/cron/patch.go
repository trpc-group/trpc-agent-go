//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

import "time"

// Patch represents optional job updates.
type Patch struct {
	Name       *string
	Message    *string
	Enabled    *bool
	Schedule   *Schedule
	TimeoutSec *int
	Channel    *string
	Target     *string
}

func applyPatch(job *Job, patch Patch, now time.Time) error {
	if patch.Name != nil {
		job.Name = *patch.Name
	}
	if patch.Message != nil {
		job.Message = *patch.Message
	}
	if patch.Enabled != nil {
		job.Enabled = *patch.Enabled
	}
	if patch.Schedule != nil {
		job.Schedule = *patch.Schedule
	}
	if patch.TimeoutSec != nil {
		job.TimeoutSec = *patch.TimeoutSec
	}
	if patch.Channel != nil {
		job.Delivery.Channel = *patch.Channel
	}
	if patch.Target != nil {
		job.Delivery.Target = *patch.Target
	}
	job.UpdatedAt = now

	if _, err := normalizeCommon(job, false, now); err != nil {
		return err
	}
	if !job.Enabled {
		job.NextRunAt = nil
	}
	return nil
}
