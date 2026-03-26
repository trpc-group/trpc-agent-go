//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	gwclientStatusErrFmt = "gwclient: status %d"

	gwclientStatusAPIErrorFmt = "gwclient: status %d: %s: %s"

	errNilGatewayServer = "gateway client: nil server"
	errNilCronService   = "gateway client: cron service unavailable"
	errNilPersonaStore  = "gateway client: persona store unavailable"
	errUnknownJob       = "gateway client: unknown scheduled job"

	debugTraceMetaFile = "meta.json"

	errEmptyForgetChannel = "gateway client: empty forget channel"
	errEmptyForgetUserID  = "gateway client: empty forget user id"
)

type inProcGatewayClient struct {
	srv      *gateway.Server
	appName  string
	sessions session.Service
	memories memory.Service
	cronSvc  *cron.Service

	debugDir        string
	uploads         *uploads.Store
	personas        *persona.Store
	memoryFileStore *memoryfile.Store
}

func newInProcGatewayClient(
	srv *gateway.Server,
	appName string,
	sessions session.Service,
	memories memory.Service,
	debugDir string,
	uploadStores ...*uploads.Store,
) *inProcGatewayClient {
	var uploadStore *uploads.Store
	if len(uploadStores) > 0 {
		uploadStore = uploadStores[0]
	}
	return &inProcGatewayClient{
		srv:      srv,
		appName:  strings.TrimSpace(appName),
		sessions: sessions,
		memories: memories,
		debugDir: strings.TrimSpace(debugDir),
		uploads:  uploadStore,
	}
}

func (c *inProcGatewayClient) SetCronService(svc *cron.Service) {
	if c == nil {
		return
	}
	c.cronSvc = svc
}

func (c *inProcGatewayClient) SetPersonaStore(store *persona.Store) {
	if c == nil {
		return
	}
	c.personas = store
}

func (c *inProcGatewayClient) SetMemoryFileStore(store *memoryfile.Store) {
	if c == nil {
		return
	}
	c.memoryFileStore = store
}

func (c *inProcGatewayClient) SendMessage(
	ctx context.Context,
	req gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	if c == nil || c.srv == nil {
		return gwclient.MessageResponse{}, errors.New(errNilGatewayServer)
	}

	rsp, status := c.srv.ProcessMessage(ctx, req)
	out := gwclient.MessageResponse{
		SessionID:  rsp.SessionID,
		RequestID:  rsp.RequestID,
		Reply:      rsp.Reply,
		Ignored:    rsp.Ignored,
		Error:      rsp.Error,
		StatusCode: status,
	}
	if err := errorForGWStatus(status, out.Error); err != nil {
		return out, err
	}
	return out, nil
}

func (c *inProcGatewayClient) StreamMessage(
	ctx context.Context,
	req gwclient.MessageRequest,
) (<-chan gwclient.StreamEvent, error) {
	if c == nil || c.srv == nil {
		return nil, errors.New(errNilGatewayServer)
	}

	stream, apiErr, status := c.srv.StreamMessage(ctx, req)
	if err := errorForGWStatus(status, apiErr); err != nil {
		return nil, err
	}
	return stream, nil
}

func (c *inProcGatewayClient) Cancel(
	ctx context.Context,
	requestID string,
) (bool, error) {
	if c == nil || c.srv == nil {
		return false, errors.New(errNilGatewayServer)
	}

	canceled, apiErr, status := c.srv.CancelRequest(ctx, requestID)
	if err := errorForGWStatus(status, apiErr); err != nil {
		return false, err
	}
	return canceled, nil
}

func (c *inProcGatewayClient) ForgetUser(
	ctx context.Context,
	channel string,
	userID string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.srv == nil {
		return errors.New(errNilGatewayServer)
	}

	channel = strings.TrimSpace(channel)
	if channel == "" {
		return errors.New(errEmptyForgetChannel)
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New(errEmptyForgetUserID)
	}
	appName := strings.TrimSpace(c.appName)
	if appName == "" {
		return session.ErrAppNameRequired
	}

	if c.cronSvc != nil {
		if _, err := c.cronSvc.RemoveForUser(
			userID,
			outbound.DeliveryTarget{},
		); err != nil {
			return fmt.Errorf("forget: clear scheduled jobs: %w", err)
		}
	}

	if c.sessions != nil {
		userKey := session.UserKey{AppName: appName, UserID: userID}
		sessions, err := c.sessions.ListSessions(ctx, userKey)
		if err != nil {
			return fmt.Errorf("forget: list sessions: %w", err)
		}
		for _, sess := range sessions {
			if sess == nil || strings.TrimSpace(sess.ID) == "" {
				continue
			}
			if cron.IsRunSessionID(sess.ID) {
				continue
			}
			key := session.Key{
				AppName:   appName,
				UserID:    userID,
				SessionID: sess.ID,
			}
			if err := c.sessions.DeleteSession(ctx, key); err != nil {
				return fmt.Errorf("forget: delete session: %w", err)
			}
		}
	}

	if c.memories != nil {
		userKey := memory.UserKey{AppName: appName, UserID: userID}
		if err := c.memories.ClearMemories(ctx, userKey); err != nil {
			return fmt.Errorf("forget: clear memories: %w", err)
		}
	}

	if c.uploads != nil {
		if err := c.uploads.DeleteUser(ctx, channel, userID); err != nil {
			return fmt.Errorf("forget: delete uploads: %w", err)
		}
	}
	if c.personas != nil {
		if err := c.personas.ForgetUser(ctx, channel, userID); err != nil {
			return fmt.Errorf("forget: delete personas: %w", err)
		}
	}
	if c.memoryFileStore != nil {
		if err := c.memoryFileStore.DeleteUser(ctx, appName, userID); err != nil {
			return fmt.Errorf("forget: delete user memory files: %w", err)
		}
	}

	if err := deleteDebugTraces(
		ctx,
		c.debugDir,
		channel,
		appName,
		userID,
	); err != nil {
		return fmt.Errorf("forget: delete debug traces: %w", err)
	}

	return nil
}

func (c *inProcGatewayClient) ListPresetPersonas() []persona.Preset {
	return persona.List()
}

func (c *inProcGatewayClient) GetPresetPersona(
	_ context.Context,
	scopeKey string,
) (persona.Preset, error) {
	if c == nil || c.personas == nil {
		return persona.DefaultPreset(), nil
	}
	return c.personas.Get(scopeKey)
}

func (c *inProcGatewayClient) SetPresetPersona(
	ctx context.Context,
	scopeKey string,
	presetID string,
) (persona.Preset, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.personas == nil {
		return persona.Preset{}, errors.New(errNilPersonaStore)
	}
	return c.personas.Set(ctx, scopeKey, presetID)
}

func (c *inProcGatewayClient) ListScheduledJobs(
	_ context.Context,
	channel string,
	userID string,
	target string,
) ([]gwclient.ScheduledJobSummary, error) {
	if c == nil || c.cronSvc == nil {
		return nil, errors.New(errNilCronService)
	}

	jobs := c.cronSvc.ListForUser(
		userID,
		cronDeliveryTarget(channel, target),
	)
	out := make([]gwclient.ScheduledJobSummary, 0, len(jobs))
	for _, job := range jobs {
		if job == nil {
			continue
		}
		out = append(out, summarizeScheduledJob(job))
	}
	return out, nil
}

func (c *inProcGatewayClient) ClearScheduledJobs(
	_ context.Context,
	channel string,
	userID string,
	target string,
) (int, error) {
	if c == nil || c.cronSvc == nil {
		return 0, errors.New(errNilCronService)
	}
	return c.cronSvc.RemoveForUser(
		userID,
		cronDeliveryTarget(channel, target),
	)
}

func (c *inProcGatewayClient) SetScheduledJobEnabled(
	_ context.Context,
	channel string,
	userID string,
	target string,
	jobID string,
	enabled bool,
) (gwclient.ScheduledJobSummary, error) {
	if c == nil || c.cronSvc == nil {
		return gwclient.ScheduledJobSummary{},
			errors.New(errNilCronService)
	}

	job, err := c.scopedScheduledJob(
		channel,
		userID,
		target,
		jobID,
	)
	if err != nil {
		return gwclient.ScheduledJobSummary{}, err
	}

	updated, err := c.cronSvc.Update(
		job.ID,
		cron.Patch{Enabled: &enabled},
	)
	if err != nil {
		return gwclient.ScheduledJobSummary{}, err
	}
	return summarizeScheduledJob(updated), nil
}

func (c *inProcGatewayClient) RemoveScheduledJob(
	_ context.Context,
	channel string,
	userID string,
	target string,
	jobID string,
) (bool, error) {
	if c == nil || c.cronSvc == nil {
		return false, errors.New(errNilCronService)
	}

	job, err := c.scopedScheduledJob(
		channel,
		userID,
		target,
		jobID,
	)
	if err != nil {
		return false, err
	}

	if err := c.cronSvc.Remove(job.ID); err != nil {
		return false, err
	}
	return true, nil
}

func errorForGWStatus(status int, apiErr *gwclient.APIError) error {
	if status == http.StatusOK {
		return nil
	}
	if apiErr == nil {
		return fmt.Errorf(gwclientStatusErrFmt, status)
	}
	return fmt.Errorf(
		gwclientStatusAPIErrorFmt,
		status,
		apiErr.Type,
		apiErr.Message,
	)
}

func cronDeliveryTarget(
	channel string,
	target string,
) outbound.DeliveryTarget {
	return outbound.DeliveryTarget{
		Channel: strings.TrimSpace(channel),
		Target:  strings.TrimSpace(target),
	}
}

func summarizeScheduledJob(job *cron.Job) gwclient.ScheduledJobSummary {
	return gwclient.ScheduledJobSummary{
		ID:               job.ID,
		Name:             job.Name,
		Enabled:          job.Enabled,
		Schedule:         jobScheduleSummary(job.Schedule),
		Message:          job.Message,
		MaxRuns:          job.Policy.MaxRuns,
		RunCount:         job.Stats.RunCount,
		SuccessCount:     job.Stats.SuccessCount,
		FailureCount:     job.Stats.FailureCount,
		DeliveryFailures: job.Stats.DeliveryFailureCount,
		EndsAt:           cloneTime(job.Policy.EndsAt),
		OverlapPolicy:    job.Policy.OverlapPolicy,
		NextRunAt:        cloneTime(job.NextRunAt),
		LastStatus:       job.LastStatus,
		LastError:        job.LastError,
		LastOutput:       job.LastOutput,
		DeliveryChannel:  job.Delivery.Channel,
		DeliveryTarget:   job.Delivery.Target,
	}
}

func (c *inProcGatewayClient) scopedScheduledJob(
	channel string,
	userID string,
	target string,
	jobID string,
) (*cron.Job, error) {
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return nil, fmt.Errorf("%s: empty id", errUnknownJob)
	}

	jobs := c.cronSvc.ListForUser(
		userID,
		cronDeliveryTarget(channel, target),
	)
	for _, job := range jobs {
		if job == nil {
			continue
		}
		if strings.TrimSpace(job.ID) == trimmedID {
			return job, nil
		}
	}
	return nil, fmt.Errorf("%s: %s", errUnknownJob, trimmedID)
}

func jobScheduleSummary(schedule cron.Schedule) string {
	return cron.ScheduleSummary(schedule)
}

func cloneTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	next := *src
	return &next
}

type traceMeta struct {
	Start debugrecorder.TraceStart `json:"start"`
}

func deleteDebugTraces(
	ctx context.Context,
	debugDir string,
	channel string,
	appName string,
	userID string,
) error {
	debugDir = strings.TrimSpace(debugDir)
	if debugDir == "" {
		return nil
	}

	st, err := os.Stat(debugDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !st.IsDir() {
		return nil
	}

	var dirs []string
	walkErr := filepath.WalkDir(
		debugDir,
		func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Name() != debugTraceMetaFile {
				return nil
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			var meta traceMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				return nil
			}

			if strings.TrimSpace(meta.Start.AppName) != appName {
				return nil
			}
			if strings.TrimSpace(meta.Start.Channel) != channel {
				return nil
			}
			if strings.TrimSpace(meta.Start.UserID) != userID {
				return nil
			}

			dir := filepath.Dir(path)
			if filepath.Clean(dir) == filepath.Clean(debugDir) {
				return nil
			}
			dirs = append(dirs, dir)
			return nil
		},
	)
	if walkErr != nil {
		return walkErr
	}

	if len(dirs) == 0 {
		return nil
	}

	sort.Strings(dirs)
	dirs = compactStrings(dirs)

	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

func compactStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := make([]string, 0, len(in))
	prev := in[0]
	out = append(out, prev)
	for _, v := range in[1:] {
		if v == prev {
			continue
		}
		out = append(out, v)
		prev = v
	}
	return out
}
