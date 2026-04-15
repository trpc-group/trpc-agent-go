//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

type stubRunner struct {
	reply string
}

type stubBMP struct {
	status BrowserManagedService
}

type stubSkillsProvider struct {
	report        ocskills.StatusReport
	err           error
	statusCount   int
	configPath    string
	refreshable   bool
	refreshCount  int
	refreshErr    error
	lastConfigKey string
	lastEnabled   bool
	setCount      int
	setErr        error
}

type stubPromptsProvider struct {
	status PromptsStatus
	err    error

	inlineErr  error
	runtimeErr error
	fileErr    error
	createErr  error
	deleteErr  error

	inlineBundle string
	inlineValue  string
	inlineCount  int

	runtimeBundle string
	runtimeValue  string
	runtimeCount  int

	fileBundle string
	filePath   string
	fileValue  string
	fileCount  int

	createBundle string
	createName   string
	createValue  string
	createCount  int

	deleteBundle string
	deletePath   string
	deleteCount  int
}

type stubIdentityProvider struct {
	status IdentityStatus
	err    error

	saveErr   error
	saveName  string
	saveCount int
}

type stubPersonasProvider struct {
	status PersonasStatus
	err    error

	defaultErr error
	saveErr    error
	deleteErr  error

	defaultPersona string
	defaultCount   int

	storeKey    string
	personaID   string
	personaName string
	personaBody string
	saveCount   int

	deleteStore string
	deleteID    string
	deleteCount int
}

type stubChatsProvider struct {
	status ChatsStatus
	err    error

	history       ChatHistoryPage
	historyErr    error
	historyChatID string
	historyCursor string
	historyCount  int
	detail        ChatView
	detailRaw     bool
	detailErr     error
	detailChatID  string
	detailCount   int
}

type stubStatusOnlyChatsProvider struct {
	status ChatsStatus
	err    error
}

func (p stubBMP) BrowserManagedStatus() BrowserManagedService {
	return p.status
}

func (p *stubSkillsProvider) SkillsStatus() (ocskills.StatusReport, error) {
	if p != nil {
		p.statusCount++
	}
	return p.report, p.err
}

func (p *stubSkillsProvider) SkillsConfigPath() string {
	if p == nil {
		return ""
	}
	return p.configPath
}

func (p *stubSkillsProvider) SkillsRefreshable() bool {
	if p == nil {
		return false
	}
	return p.refreshable
}

func (p *stubSkillsProvider) RefreshSkills() error {
	if p == nil {
		return nil
	}
	p.refreshCount++
	return p.refreshErr
}

func (p *stubSkillsProvider) SetSkillEnabled(
	configKey string,
	enabled bool,
) error {
	if p == nil {
		return nil
	}
	p.setCount++
	p.lastConfigKey = configKey
	p.lastEnabled = enabled
	return p.setErr
}

func (p *stubPromptsProvider) PromptsStatus() (
	PromptsStatus,
	error,
) {
	if p == nil {
		return PromptsStatus{}, nil
	}
	return p.status, p.err
}

func (p *stubPromptsProvider) SavePromptRuntime(
	bundleKey string,
	content string,
) error {
	if p == nil {
		return nil
	}
	p.runtimeCount++
	p.runtimeBundle = bundleKey
	p.runtimeValue = content
	return p.runtimeErr
}

func (p *stubPromptsProvider) SavePromptInline(
	bundleKey string,
	content string,
) error {
	if p == nil {
		return nil
	}
	p.inlineCount++
	p.inlineBundle = bundleKey
	p.inlineValue = content
	return p.inlineErr
}

func (p *stubPromptsProvider) SavePromptFile(
	bundleKey string,
	path string,
	content string,
) error {
	if p == nil {
		return nil
	}
	p.fileCount++
	p.fileBundle = bundleKey
	p.filePath = path
	p.fileValue = content
	return p.fileErr
}

func (p *stubPromptsProvider) CreatePromptFile(
	bundleKey string,
	fileName string,
	content string,
) error {
	if p == nil {
		return nil
	}
	p.createCount++
	p.createBundle = bundleKey
	p.createName = fileName
	p.createValue = content
	return p.createErr
}

func (p *stubPromptsProvider) DeletePromptFile(
	bundleKey string,
	path string,
) error {
	if p == nil {
		return nil
	}
	p.deleteCount++
	p.deleteBundle = bundleKey
	p.deletePath = path
	return p.deleteErr
}

func (p *stubChatsProvider) ChatsStatus() (ChatsStatus, error) {
	if p == nil {
		return ChatsStatus{}, nil
	}
	return p.status, p.err
}

func (p *stubStatusOnlyChatsProvider) ChatsStatus() (
	ChatsStatus,
	error,
) {
	if p == nil {
		return ChatsStatus{}, nil
	}
	return p.status, p.err
}

func (p *stubChatsProvider) ChatDetail(
	baseSessionID string,
) (ChatView, error) {
	if p == nil {
		return ChatView{}, nil
	}
	p.detailCount++
	p.detailChatID = baseSessionID
	if p.detailErr != nil {
		return ChatView{}, p.detailErr
	}
	if p.detailRaw {
		return p.detail, nil
	}
	if strings.TrimSpace(p.detail.BaseSessionID) ==
		strings.TrimSpace(baseSessionID) {
		return p.detail, nil
	}
	for _, chat := range p.status.Chats {
		if chat.BaseSessionID == baseSessionID {
			return chat, nil
		}
	}
	return ChatView{}, nil
}

func (p *stubChatsProvider) ChatHistory(
	baseSessionID string,
	cursor string,
) (ChatHistoryPage, error) {
	if p == nil {
		return ChatHistoryPage{}, nil
	}
	p.historyCount++
	p.historyChatID = baseSessionID
	p.historyCursor = cursor
	if p.historyErr != nil {
		return ChatHistoryPage{}, p.historyErr
	}
	return p.history, nil
}

func (p *stubIdentityProvider) IdentityStatus() (
	IdentityStatus,
	error,
) {
	if p == nil {
		return IdentityStatus{}, nil
	}
	return p.status, p.err
}

func (p *stubIdentityProvider) SaveAssistantName(
	name string,
) error {
	if p != nil {
		p.saveCount++
		p.saveName = name
	}
	return p.saveErr
}

func (p *stubPersonasProvider) PersonasStatus() (
	PersonasStatus,
	error,
) {
	if p == nil {
		return PersonasStatus{}, nil
	}
	return p.status, p.err
}

func (p *stubPersonasProvider) SavePersona(
	storeKey string,
	personaID string,
	name string,
	prompt string,
) error {
	if p == nil {
		return nil
	}
	p.saveCount++
	p.storeKey = storeKey
	p.personaID = personaID
	p.personaName = name
	p.personaBody = prompt
	return p.saveErr
}

func (p *stubPersonasProvider) DeletePersona(
	storeKey string,
	personaID string,
) error {
	if p == nil {
		return nil
	}
	p.deleteCount++
	p.deleteStore = storeKey
	p.deleteID = personaID
	return p.deleteErr
}

func (p *stubPersonasProvider) SetDefaultPersona(
	personaID string,
) error {
	if p == nil {
		return nil
	}
	p.defaultCount++
	p.defaultPersona = personaID
	return p.defaultErr
}

func (r *stubRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(r.reply),
			}},
			Done: true,
		},
	}
	close(ch)
	return ch, nil
}

func (r *stubRunner) Close() error { return nil }

func writeDebugTraceFixture(
	t *testing.T,
	root string,
	sessionID string,
	requestID string,
	startedAt time.Time,
	traceID string,
) string {
	t.Helper()

	traceDir := filepath.Join(
		root,
		startedAt.Format("20060102"),
		startedAt.Format("150405")+"_"+requestID,
	)
	require.NoError(t, os.MkdirAll(traceDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(traceDir, debugMetaFileName),
		[]byte(
			`{"request_id":"`+requestID+`","trace_id":"`+
				traceID+`"}`+"\n",
		),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(traceDir, debugEventsFileName),
		[]byte(`{"kind":"trace.start"}`+"\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(traceDir, debugResultFileName),
		[]byte(`{"status":"ok"}`+"\n"),
		0o600,
	))

	indexDir := filepath.Join(
		root,
		debugBySessionDir,
		sessionID,
		startedAt.Format("20060102"),
		startedAt.Format("150405")+"_"+requestID,
	)
	require.NoError(t, os.MkdirAll(indexDir, 0o755))
	rel, err := filepath.Rel(indexDir, traceDir)
	require.NoError(t, err)
	ref := `{"trace_dir":"` + filepath.ToSlash(rel) + `",` +
		`"started_at":"` + startedAt.Format(time.RFC3339Nano) + `",` +
		`"channel":"telegram","request_id":"` + requestID + `",` +
		`"message_id":"msg-` + requestID + `","trace_id":"` +
		traceID + `"}`
	require.NoError(t, os.WriteFile(
		filepath.Join(indexDir, debugMetaTraceRefName),
		[]byte(ref),
		0o600,
	))
	return traceDir
}

func gzipDebugTraceEventsFixture(t *testing.T, traceDir string) {
	t.Helper()

	eventsPath := filepath.Join(traceDir, debugEventsFileName)
	raw, err := os.ReadFile(eventsPath)
	require.NoError(t, err)

	gzipPath := eventsPath + ".gz"
	file, err := os.OpenFile(
		gzipPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o600,
	)
	require.NoError(t, err)

	writer := gzip.NewWriter(file)
	_, err = writer.Write(raw)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	require.NoError(t, os.Remove(eventsPath))
}

func TestNormalizeLangfuseStatus_TrimsValues(t *testing.T) {
	t.Parallel()

	got := normalizeLangfuseStatus(LangfuseStatus{
		Error:            " boom ",
		UIBaseURL:        " http://127.0.0.1:3000/ ",
		TraceURLTemplate: " http://127.0.0.1:3000/traces/{{trace_id}} ",
	})

	require.Equal(t, "boom", got.Error)
	require.Equal(t, "http://127.0.0.1:3000", got.UIBaseURL)
	require.Equal(
		t,
		"http://127.0.0.1:3000/traces/{{trace_id}}",
		got.TraceURLTemplate,
	)
}

func TestService_LangfuseTraceURL_Guards(t *testing.T) {
	t.Parallel()

	var nilSvc *Service
	require.Empty(t, nilSvc.langfuseTraceURL("trace-1"))

	svc := New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          true,
			Ready:            true,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/{{trace_id}}",
		},
	})
	require.Equal(
		t,
		"http://127.0.0.1:3000/traces/trace-1",
		svc.langfuseTraceURL(" trace-1 "),
	)

	svc = New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          false,
			Ready:            true,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/{{trace_id}}",
		},
	})
	require.Empty(t, svc.langfuseTraceURL("trace-1"))

	svc = New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          true,
			Ready:            false,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/{{trace_id}}",
		},
	})
	require.Empty(t, svc.langfuseTraceURL("trace-1"))

	svc = New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          true,
			Ready:            true,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/static",
		},
	})
	require.Empty(t, svc.langfuseTraceURL("trace-1"))
}

func TestServiceHandlerRendersOverview(t *testing.T) {
	t.Parallel()

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	_, err = cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu and mem",
		UserID:  "u1",
	})
	require.NoError(t, err)

	svc := New(Config{
		AppName:     "openclaw",
		InstanceID:  "abcd1234",
		GatewayAddr: "127.0.0.1:8080",
		GatewayURL:  "http://127.0.0.1:8080",
		AdminAddr:   "127.0.0.1:18789",
		AdminURL:    "http://127.0.0.1:18789/",
		StateDir:    "/tmp/openclaw",
		DebugDir:    "/tmp/openclaw/debug",
		Channels:    []string{"telegram"},
		Cron:        cronSvc,
	})

	req := httptest.NewRequest(http.MethodGet, routeIndex, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "TRPC-CLAW admin")
	require.Contains(t, body, "TRPC-CLAW")
	require.Contains(t, body, "trpc-claw")
	require.Contains(t, body, `href="skills"`)
	require.Contains(t, body, `action="api/cron/jobs/clear"`)
	require.Contains(t, body, "127.0.0.1:8080")
	require.Contains(t, body, "telegram")
	require.Contains(t, body, "Refresh page")
	require.Contains(t, body, `data-page-stale-root`)
	require.NotContains(t, body, `http-equiv="refresh"`)
}

func TestServiceHandlerRendersSkillsInventory(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		AppName: "openclaw",
		Skills: &stubSkillsProvider{
			configPath:  "/tmp/openclaw.yaml",
			refreshable: true,
			report: ocskills.StatusReport{
				Skills: []ocskills.StatusEntry{{
					Name:        "weather-probe",
					Description: "Probe weather prerequisites",
					SkillKey:    "weather-api",
					ConfigKey:   "weather-api",
					FilePath:    "/tmp/skills/weather-probe",
					Source:      "bundled",
					Reason:      "missing env: OPENAI_API_KEY",
					PrimaryEnv:  "OPENAI_API_KEY",
					Bundled:     true,
				}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, routeSkillsPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Skills Inventory")
	require.Contains(t, body, "weather-probe")
	require.Contains(t, body, `action="api/skills/refresh"`)
	require.Contains(t, body, `action="api/skills/toggle"`)
	require.Contains(t, body, `href="overview"`)
	require.Contains(t, body, `href="skills"`)
	require.Contains(t, body, "/tmp/openclaw.yaml")
	require.Contains(t, body, "OPENAI_API_KEY")
}

func TestServiceHandlerRendersMemoryInventory(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	_, err = store.UpdateMemory(
		context.Background(),
		"openclaw",
		"alice",
		func(string) (string, error) {
			return "# Memory\n\n- Alice prefers concise updates.\n", nil
		},
	)
	require.NoError(t, err)

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})

	req := httptest.NewRequest(http.MethodGet, routeMemory, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Memory Files")
	require.Contains(t, body, "openclaw")
	require.Contains(t, body, "alice")
	require.Contains(t, body, "Alice prefers concise updates.")
	require.Contains(t, body, `href="api/memory/files"`)
	require.Contains(t, body, `href="memory/file?path=`)
	require.Contains(t, body, `data-memory-root`)
	require.Contains(t, body, `data-memory-search`)
	require.Contains(t, body, `data-memory-row`)
	require.Contains(t, body, `data-memory-app="openclaw"`)
	require.Contains(t, body, `data-memory-user="alice"`)
	require.Contains(t, body, `data-memory-search="alice`)
	require.Contains(t, body, "Search users or memory content")
}

func TestServiceHandlerRendersMemoryInventory_FileBackendUnconfigured(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		MemoryBackend: "file",
	})

	req := httptest.NewRequest(http.MethodGet, routeMemory, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "file backend not configured")
	require.Contains(t, body, "File-backed memory store is not configured for this runtime.")
	require.Contains(t, body, "not configured")
	require.NotContains(t, body, "Structured memory service")
}

func TestServiceMemoryFilesJSONEndpoint(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	_, err = store.UpdateMemory(
		context.Background(),
		"openclaw",
		"alice",
		func(string) (string, error) {
			return "# Memory\n\n- Alice prefers concise updates.\n", nil
		},
	)
	require.NoError(t, err)

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})

	req := httptest.NewRequest(http.MethodGet, routeMemoryFilesJSON, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, `"file_count": 1`)
	require.Contains(t, body, `"app_name": "openclaw"`)
	require.Contains(t, body, `"user_id": "alice"`)
}

func TestServiceMemoryFilesJSONEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, routeMemoryFilesJSON, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestServiceMemoryFileEndpoint(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	_, err = store.UpdateMemory(
		context.Background(),
		"openclaw",
		"alice",
		func(string) (string, error) {
			return "# Memory\n\n- Alice prefers concise updates.\n", nil
		},
	)
	require.NoError(t, err)

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})
	status := svc.memoryStatus()
	require.Len(t, status.Files, 1)

	req := httptest.NewRequest(
		http.MethodGet,
		routeMemoryFile+"?path="+url.QueryEscape(
			status.Files[0].RelativePath,
		),
		nil,
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), "Alice prefers concise updates.")
}

func TestServiceMemoryFileEndpointRequiresStore(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	req := httptest.NewRequest(
		http.MethodGet,
		routeMemoryFile+"?path=app%2Fuser%2FMEMORY.md",
		nil,
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "not configured")
}

func TestServiceMemoryFileEndpointRejectsTypedNilStore(t *testing.T) {
	t.Parallel()

	var typedNil *memoryfile.Store
	var store MemoryFileStore = typedNil

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})
	req := httptest.NewRequest(
		http.MethodGet,
		routeMemoryFile+"?path=app%2Fuser%2FMEMORY.md",
		nil,
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "not configured")
}

func TestServiceMemoryFileEndpoint_MethodAndPathValidation(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})

	req := httptest.NewRequest(http.MethodPost, routeMemoryFile, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)

	req = httptest.NewRequest(
		http.MethodGet,
		routeMemoryFile+"?path="+url.QueryEscape(filepath.Join(root, "app", "user", "MEMORY.md")),
		nil,
	)
	rr = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid memory file path")
}

func TestResolveMemoryFileGuards(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "app", "user")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "MEMORY.md")
	require.NoError(t, os.WriteFile(path, []byte("# Memory\n"), 0o600))

	got, err := resolveMemoryFile(root, "app/user/MEMORY.md")
	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	require.Equal(t, expected, got)

	_, err = resolveMemoryFile("", "app/user/MEMORY.md")
	require.ErrorContains(t, err, "not configured")

	_, err = resolveMemoryFile(root, "")
	require.ErrorContains(t, err, "required")

	_, err = resolveMemoryFile(root, "../MEMORY.md")
	require.ErrorContains(t, err, "invalid memory file path")

	_, err = resolveMemoryFile(root, "app/user/notes.md")
	require.ErrorContains(t, err, "unsupported memory file")

	_, err = resolveMemoryFile(root, "app/user/missing/MEMORY.md")
	require.ErrorContains(t, err, "memory file not found")
}

func TestSummarizeMemoryPreview(t *testing.T) {
	t.Parallel()

	input := "# Memory\n\n- first\n- second\n- third\n- fourth\n"
	got := summarizeMemoryPreview(input, 3, 220)
	require.Equal(t, "- first\n- second\n- third...", got)

	got = summarizeMemoryPreview("# Memory\n\n- only one\n", 3, 220)
	require.Equal(t, "- only one", got)

	got = summarizeMemoryPreview("# Memory\n\n", 3, 220)
	require.Empty(t, got)
}

func TestServiceSnapshotOmitsMemoryFilesFromStatus(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	_, err = store.UpdateMemory(
		context.Background(),
		"openclaw",
		"alice",
		func(string) (string, error) {
			return "# Memory\n\n- Alice prefers concise updates.\n", nil
		},
	)
	require.NoError(t, err)

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})

	snap := svc.Snapshot()
	require.Equal(t, 1, snap.Memory.FileCount)
	require.Len(t, snap.Memory.Files, 0)

	viewSnap := svc.snapshotForView(viewMemory)
	require.Len(t, viewSnap.Memory.Files, 1)
	require.Equal(t, "alice", viewSnap.Memory.Files[0].UserID)
}

func TestServiceRenderPageScopesSnapshotToActiveView(t *testing.T) {
	t.Parallel()

	var browserProfilesHits int32
	browserServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		atomic.AddInt32(&browserProfilesHits, 1)
		require.Equal(t, "/profiles", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"profiles":[]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(browserServer.Close)

	provider := &stubSkillsProvider{
		report: ocskills.StatusReport{
			Skills: []ocskills.StatusEntry{{
				Name:      "weather-probe",
				ConfigKey: "weather-probe",
				Eligible:  true,
			}},
		},
	}
	svc := New(
		Config{
			Skills: provider,
			Browser: BrowserConfig{
				Providers: []BrowserProvider{{
					Name:          "browser",
					HostServerURL: browserServer.URL,
				}},
			},
		},
		WithBrowserHTTPClient(browserServer.Client()),
	)

	req := httptest.NewRequest(http.MethodGet, routeSkillsPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, provider.statusCount)
	require.Zero(t, atomic.LoadInt32(&browserProfilesHits))

	req = httptest.NewRequest(http.MethodGet, routeBrowser, nil)
	rr = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, provider.statusCount)
	require.NotZero(t, atomic.LoadInt32(&browserProfilesHits))
}

func TestServiceHandlerRendersAdminPages(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	handler := svc.Handler()

	cases := []struct {
		name    string
		path    string
		title   string
		summary string
	}{
		{
			name:    "memory",
			path:    routeMemory,
			title:   "Memory",
			summary: "Inspect durable memory storage, file-backed MEMORY.md scopes, and memory inventory.",
		},
		{
			name:    "automation",
			path:    routeAutomation,
			title:   "Automation",
			summary: "Inspect scheduled jobs, trigger one-off runs, and clear automation state.",
		},
		{
			name:    "sessions",
			path:    routeSessions,
			title:   "Sessions",
			summary: "Review exec sessions, upload sessions, and recently persisted files.",
		},
		{
			name:    "debug",
			path:    routeDebug,
			title:   "Debug",
			summary: "Browse debug session indexes, recent traces, and Langfuse readiness.",
		},
		{
			name:    "browser",
			path:    routeBrowser,
			title:   "Browser",
			summary: "Inspect browser providers, managed browser-server state, nodes, and profiles.",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			require.Contains(t, rr.Body.String(), tc.title)
			require.Contains(t, rr.Body.String(), tc.summary)
		})
	}
}

func TestServiceOverviewConfigNavVisibility(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, routeOverview, nil)

	hidden := New(Config{})
	hiddenRec := httptest.NewRecorder()
	hidden.Handler().ServeHTTP(hiddenRec, req)
	require.Equal(t, http.StatusOK, hiddenRec.Code)
	require.NotContains(t, hiddenRec.Body.String(), `href="config"`)

	visible := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			status: RuntimeConfigStatus{
				ConfigPath: "/tmp/openclaw.yaml",
			},
		}),
	)
	visibleRec := httptest.NewRecorder()
	visible.Handler().ServeHTTP(visibleRec, req)
	require.Equal(t, http.StatusOK, visibleRec.Code)
	require.Contains(t, visibleRec.Body.String(), `href="config"`)
}

func TestServiceRenderPageRejectsNonGET(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, routeOverview, nil)
	rr := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestServicePageStateJSONEndpointStableAcrossClockTicks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 13, 21, 0, 0, 0, time.UTC)
	svc := New(
		Config{
			AppName: "openclaw",
			Chats: &stubChatsProvider{
				status: ChatsStatus{
					Enabled: true,
					Chats: []ChatView{{
						BaseSessionID:      "wecom:dm:T00320026A",
						DisplayLabel:       "DM · wineguo (T00320026A)",
						EffectiveAssistant: "winechord",
					}},
				},
			},
		},
		WithClock(func() time.Time {
			return now
		}),
	)

	handler := svc.Handler()
	path := routePageStateJSON + "?" + url.Values{
		queryView: []string{string(viewChats)},
	}.Encode()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var first pageStateStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	require.NotEmpty(t, first.Token)
	require.Equal(t, now, first.UpdatedAt)

	now = now.Add(30 * time.Second)
	req = httptest.NewRequest(http.MethodGet, path, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var second pageStateStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &second))
	require.Equal(t, first.Token, second.Token)
	require.Equal(t, now, second.UpdatedAt)
}

func TestServiceSkillsStatusErrorRetainsRecoveryFields(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Skills: &stubSkillsProvider{
			configPath:  "/tmp/openclaw.yaml",
			refreshable: true,
			err:         errors.New("skills status boom"),
		},
	})

	status := svc.skillsStatus()
	require.True(t, status.Enabled)
	require.True(t, status.Writable)
	require.True(t, status.Refreshable)
	require.Equal(t, "/tmp/openclaw.yaml", status.ConfigPath)
	require.Equal(t, "skills status boom", status.Error)
}

func TestServiceHandlerSuppressesSkillsEmptyStateOnError(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Skills: &stubSkillsProvider{
			configPath:  "/tmp/openclaw.yaml",
			refreshable: true,
			err:         errors.New("skills status boom"),
		},
	})

	req := httptest.NewRequest(http.MethodGet, routeSkillsPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "skills status boom")
	require.NotContains(t, body, "No skills discovered.")
}

func TestServiceSkillsJSONEndpoint(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Skills: &stubSkillsProvider{
			report: ocskills.StatusReport{
				Skills: []ocskills.StatusEntry{{
					Name:        "weather-probe",
					Description: "Probe weather prerequisites",
					Bundled:     true,
					Eligible:    true,
				}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, routeSkillsJSON, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"total_count": 1`)
	require.Contains(t, rr.Body.String(), `"label": "Bundled Skills"`)
	require.Contains(t, rr.Body.String(), `"name": "weather-probe"`)
}

func TestServiceSkillsJSONEndpointRejectsMethod(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, routeSkillsJSON, nil)
	rr := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestServiceToggleSkillEndpoint(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{
		configPath:  "/tmp/openclaw.yaml",
		refreshable: true,
	}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillToggle,
		strings.NewReader(
			"skill_key=weather-api&skill_name=weather-probe&enabled=false&return_to=skill-card-weather-api&return_path=%2Fskills",
		),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, "weather-api", provider.lastConfigKey)
	require.False(t, provider.lastEnabled)

	values := url.Values{}
	values.Set(
		queryNotice,
		"Saved weather-probe as disabled. Changes apply on the next turn.",
	)
	target := (&url.URL{
		Path:     "../../skills",
		Fragment: "skill-card-weather-api",
		RawQuery: values.Encode(),
	}).String()
	require.Equal(
		t,
		target,
		rr.Header().Get(headerLocation),
	)
}

func TestServiceRefreshSkillsEndpoint(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{refreshable: true}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillsRefresh,
		strings.NewReader("return_to=skills-admin&return_path=%2Fskills"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, 1, provider.refreshCount)

	values := url.Values{}
	values.Set(
		queryNotice,
		"Refreshed skills. New or removed skill folders "+
			"will be available on the next turn.",
	)
	target := (&url.URL{
		Path:     "../../skills",
		Fragment: "skills-admin",
		RawQuery: values.Encode(),
	}).String()
	require.Equal(
		t,
		target,
		rr.Header().Get(headerLocation),
	)
}

func TestServiceRefreshSkillsEndpointRequiresLiveRepo(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillsRefresh,
		strings.NewReader("return_to=skills-admin&return_path=%2Fskills"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Zero(t, provider.refreshCount)
	values := url.Values{}
	values.Set(queryError, "live skills repository is not available")
	target := (&url.URL{
		Path:     "../../skills",
		Fragment: "skills-admin",
		RawQuery: values.Encode(),
	}).String()
	require.Equal(
		t,
		target,
		rr.Header().Get(headerLocation),
	)
}

func TestServiceRefreshSkillsEndpointReportsProviderError(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{
		refreshable: true,
		refreshErr:  errors.New("refresh boom"),
	}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillsRefresh,
		strings.NewReader("return_to=skills-admin&return_path=%2Fskills"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, 1, provider.refreshCount)

	values := url.Values{}
	values.Set(queryError, "refresh boom")
	target := (&url.URL{
		Path:     "../../skills",
		Fragment: "skills-admin",
		RawQuery: values.Encode(),
	}).String()
	require.Equal(
		t,
		target,
		rr.Header().Get(headerLocation),
	)
}

func TestAdminHelpers_PageMetadataAndNavigation(t *testing.T) {
	t.Parallel()

	type pageCase struct {
		path    string
		view    adminView
		title   string
		summary string
	}

	cases := []pageCase{
		{
			path:    routeOverview,
			view:    viewOverview,
			title:   "Overview",
			summary: "Runtime summary, gateway surfaces, and entry points into the rest of the admin.",
		},
		{
			path:    routeSkillsPage,
			view:    viewSkills,
			title:   "Skills",
			summary: "Discover installed skills, refresh folders from disk, and manage config-backed enablement.",
		},
		{
			path:    routePrompts,
			view:    viewPrompts,
			title:   "Prompts",
			summary: pageSummaryPrompts,
		},
		{
			path:    routeIdentity,
			view:    viewIdentity,
			title:   "Identity",
			summary: pageSummaryIdentity,
		},
		{
			path:    routePersonas,
			view:    viewPersonas,
			title:   "Personas",
			summary: pageSummaryPersonas,
		},
		{
			path:    routeChats,
			view:    viewChats,
			title:   "Chats",
			summary: pageSummaryChats,
		},
		{
			path:    routeMemory,
			view:    viewMemory,
			title:   "Memory",
			summary: "Inspect durable memory storage, file-backed MEMORY.md scopes, and memory inventory.",
		},
		{
			path:    routeAutomation,
			view:    viewAutomation,
			title:   "Automation",
			summary: "Inspect scheduled jobs, trigger one-off runs, and clear automation state.",
		},
		{
			path:    routeSessions,
			view:    viewSessions,
			title:   "Runtime Sessions",
			summary: "Review exec sessions, upload sessions, and recently persisted files.",
		},
		{
			path:    routeDebug,
			view:    viewDebug,
			title:   "Debug",
			summary: "Browse debug session indexes, recent traces, and Langfuse readiness.",
		},
		{
			path:    routeBrowser,
			view:    viewBrowser,
			title:   "Browser",
			summary: "Inspect browser providers, managed browser-server state, nodes, and profiles.",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.title, pageTitle(tc.view))
			require.Equal(t, tc.summary, pageSummary(tc.view))
			require.Equal(t, tc.path, navPath(tc.path))
			require.Equal(t, tc.view, navViewForPath(tc.path))
		})
	}

	require.Equal(t, routeOverview, navPath(routeIndex))
	require.Equal(t, viewOverview, navViewForPath(routeIndex))
	require.Empty(t, navPath(" /unknown "))
	require.Equal(t, viewOverview, navViewForPath(" /unknown "))
}

func TestService_PromptsPageAndActions(t *testing.T) {
	t.Parallel()

	provider := &stubPromptsProvider{
		status: PromptsStatus{
			Enabled: true,
			Bundles: []PromptBundleState{{
				Key:             "agent_instruction",
				Title:           "Instruction",
				EffectiveValue:  "live prompt",
				ConfiguredValue: "configured prompt",
				InlineEditable:  true,
				InlineValue:     "inline prompt",
				RuntimeEditable: true,
				Files: []PromptFileState{{
					Path:    "/tmp/instruction.md",
					Label:   "instruction.md",
					Content: "body",
				}},
			}},
			Sections: []PromptSectionState{{
				Key:     "core",
				Title:   "Core Prompt",
				Summary: "These blocks shape the assistant across every turn.",
				Bundles: []PromptBundleState{{
					Key:             "agent_instruction",
					Title:           "Instruction",
					EffectiveValue:  "live prompt",
					ConfiguredValue: "configured prompt",
					InlineEditable:  true,
					InlineValue:     "inline prompt",
					RuntimeEditable: true,
				}},
			}},
			Previews: []PromptPreviewState{{
				Key:     "agent",
				Title:   "Agent Prompt",
				Summary: "The resolved instruction and system prompt text currently applied to the runtime.",
				Content: "Instruction\n===========\nlive prompt",
			}},
		},
	}
	personas := &stubPersonasProvider{
		status: PersonasStatus{
			Enabled:          true,
			DefaultPersonaID: "friendly",
			DefaultOptions: []PersonaOption{{
				ID:   "friendly",
				Name: "Friendly",
			}},
			Stores: []PersonaStoreView{{
				Key:           "agent",
				Title:         "Shared Persona Store",
				UsageLabels:   []string{"Agent Personas", "WeCom Personas 1"},
				CreateEnabled: true,
				Personas: []PersonaView{
					{
						ID:       "warm",
						Name:     "Warm",
						Summary:  "A direct custom tone.",
						Prompt:   "custom tone",
						Editable: true,
					},
					{
						ID:      "friendly",
						Name:    "Friendly",
						Summary: "Warm and approachable.",
						Prompt:  "warm tone",
						BuiltIn: true,
					},
				},
			}},
		},
	}
	svc := New(Config{
		Prompts:  provider,
		Personas: personas,
	})

	req := httptest.NewRequest(http.MethodGet, routePrompts, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Prompt Control")
	require.Contains(t, rec.Body.String(), "Core Prompt")
	require.Contains(t, rec.Body.String(), "Instruction")
	require.Contains(t, rec.Body.String(), "Final Prompt Preview")
	require.Contains(t, rec.Body.String(), "prompt-detail")
	require.Contains(t, rec.Body.String(), "Instruction Config Text")
	require.Contains(t, rec.Body.String(), "Save Config Text")
	require.Contains(t, rec.Body.String(), "Refresh page")
	require.NotContains(
		t,
		rec.Body.String(),
		`data-page-state-path="/api/page/state?view=prompts"`,
	)
	require.NotContains(t, rec.Body.String(), "Inline Source")
	require.NotContains(t, rec.Body.String(), "Agent Personas")
	require.Contains(t, rec.Body.String(), "/personas")

	values := url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptContent:   {"inline override"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptInlineSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, provider.inlineCount)
	require.Equal(t, "agent_instruction", provider.inlineBundle)
	require.Equal(t, "inline override", provider.inlineValue)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptContent:   {"override"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptRuntimeSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, provider.runtimeCount)
	require.Equal(t, "agent_instruction", provider.runtimeBundle)
	require.Equal(t, "override", provider.runtimeValue)

	values = url.Values{
		formPersonaID:  {"friendly"},
		formReturnPath: {routePersonas},
		formReturnTo:   {"personas-default"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaDefaultSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, personas.defaultCount)
	require.Equal(t, "friendly", personas.defaultPersona)

	values = url.Values{
		formPersonaStoreKey: {"agent"},
		formPersonaName:     {"Warm"},
		formPersonaPrompt:   {"custom prompt"},
		formReturnPath:      {routePersonas},
		formReturnTo:        {"persona-store-agent"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, personas.saveCount)
	require.Equal(t, "agent", personas.storeKey)
	require.Equal(t, "Warm", personas.personaName)
	require.Equal(t, "custom prompt", personas.personaBody)
}

func TestPromptCollapsedSummary(
	t *testing.T,
) {
	t.Parallel()

	require.Equal(
		t,
		"No prompt text is currently active.",
		promptCollapsedSummary(""),
	)
	require.Equal(
		t,
		"1 line. Starts with: single line",
		promptCollapsedSummary("single line"),
	)
	require.Equal(
		t,
		"2 lines. Starts with: first line",
		promptCollapsedSummary("first line\nsecond line"),
	)
}

func TestPromptHelperFunctions(t *testing.T) {
	t.Parallel()

	longLine := strings.Repeat("a", promptSummaryMaxRunes+5)
	require.Equal(
		t,
		strings.Repeat("a", promptSummaryMaxRunes)+"...",
		promptSummarySnippet(longLine),
	)
	require.Equal(
		t,
		"first real line",
		promptSummarySnippet("\n\n first   real   line \nsecond"),
	)
	require.Equal(t, "", promptSummarySnippet("\n \n\t"))

	require.Equal(t, 0, promptLineCount(""))
	require.Equal(t, 2, promptLineCount("first\nsecond\n"))

	require.Equal(
		t,
		"Text Stored In Config",
		promptInlineEditorTitle(PromptBundleState{}),
	)
	require.Equal(
		t,
		"Instruction Config Text",
		promptInlineEditorTitle(PromptBundleState{Title: "Instruction"}),
	)
	require.Contains(
		t,
		promptInlineEditorSummary(PromptBundleState{}),
		"config file",
	)
	require.Contains(
		t,
		promptRuntimeEditorSummary(PromptBundleState{}),
		"running process only",
	)
}

func TestService_IdentityPageAndActions(t *testing.T) {
	t.Parallel()

	identity := &stubIdentityProvider{
		status: IdentityStatus{
			Enabled:        true,
			ConfiguredName: "Claw",
			EffectiveName:  "Claw",
			RuntimeProduct: "trpc-claw",
			SourcePath:     "/tmp/IDENTITY.md",
		},
	}
	svc := New(Config{Identity: identity})

	req := httptest.NewRequest(http.MethodGet, routeIdentity, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Default Name")
	require.Contains(t, rec.Body.String(), "How Naming Works")
	require.Contains(t, rec.Body.String(), "trpc-claw")
	require.Contains(t, rec.Body.String(), "/api/identity")
	require.Contains(t, rec.Body.String(), "Refresh page")
	require.NotContains(
		t,
		rec.Body.String(),
		`data-page-state-path="/api/page/state?view=identity"`,
	)

	req = httptest.NewRequest(http.MethodGet, routeIdentityJSON, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "\"effective_name\"")

	values := url.Values{
		formAssistantName: {"Nora"},
		formReturnPath:    {routeIdentity},
		formReturnTo:      {"identity-global"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routeIdentitySave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, identity.saveCount)
	require.Equal(t, "Nora", identity.saveName)
}

func TestService_IdentityPageShowsChatOverrides(t *testing.T) {
	t.Parallel()

	identity := &stubIdentityProvider{
		status: IdentityStatus{
			Enabled:        true,
			ConfiguredName: "Claw",
			EffectiveName:  "Claw",
			RuntimeProduct: "trpc-claw",
			SourcePath:     "/tmp/IDENTITY.md",
		},
	}
	chats := &stubChatsProvider{
		status: ChatsStatus{
			Enabled:       true,
			OverrideCount: 1,
			Chats: []ChatView{{
				BaseSessionID:         "wecom:dm:alice",
				DisplayLabel:          "Direct Message / alice",
				EffectiveAssistant:    "林妹妹",
				ChatAssistantOverride: "林妹妹",
				OverridesGlobal:       true,
				LastActivity:          time.Unix(1700000000, 0),
			}},
		},
	}
	svc := New(Config{
		Identity: identity,
		Chats:    chats,
	})

	req := httptest.NewRequest(http.MethodGet, routeIdentity, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Chats Using Their Own Name")
	require.Contains(t, body, "Current chat name wins")
	require.Contains(t, body, "Direct Message / alice")
	require.Contains(t, body, "林妹妹")
	require.Contains(
		t,
		body,
		"chats?chat_id=wecom%3adm%3aalice",
	)
}

func TestService_IdentityPageShowsChatsError(t *testing.T) {
	t.Parallel()

	identity := &stubIdentityProvider{
		status: IdentityStatus{
			Enabled:        true,
			ConfiguredName: "Claw",
			EffectiveName:  "Claw",
			RuntimeProduct: "trpc-claw",
			SourcePath:     "/tmp/IDENTITY.md",
		},
	}
	chats := &stubChatsProvider{
		status: ChatsStatus{
			Enabled: true,
			Error:   "chat status failed",
		},
	}
	svc := New(Config{
		Identity: identity,
		Chats:    chats,
	})

	req := httptest.NewRequest(http.MethodGet, routeIdentity, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "chat status failed")
	require.NotContains(
		t,
		body,
		"No chat-specific name overrides are active.",
	)
}

func TestService_IdentityPageClearAction(t *testing.T) {
	t.Parallel()

	identity := &stubIdentityProvider{
		status: IdentityStatus{
			Enabled:        true,
			ConfiguredName: "Claw",
			EffectiveName:  "Claw",
			RuntimeProduct: "trpc-claw",
			SourcePath:     "/tmp/IDENTITY.md",
		},
	}
	svc := New(Config{Identity: identity})

	values := url.Values{
		formAssistantName: {""},
		formReturnPath:    {routeIdentity},
		formReturnTo:      {"identity-global"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeIdentitySave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, identity.saveCount)
	require.Empty(t, identity.saveName)
	require.Contains(
		t,
		rec.Header().Get("Location"),
		"Cleared+default+name.",
	)
}

func TestService_ChatsPageAndJSON(t *testing.T) {
	t.Parallel()

	chats := &stubChatsProvider{
		status: ChatsStatus{
			Enabled:             true,
			GlobalAssistantName: "Claw",
			Chats: []ChatView{{
				BaseSessionID:         "wecom:dm:alice",
				DisplayLabel:          "Direct Message / alice",
				KindLabel:             "Direct message",
				CurrentSessionID:      "wecom:dm:alice:171",
				RecallSessionID:       "wecom:dm:alice",
				LastActivity:          time.Unix(1700000000, 0),
				Epoch:                 171,
				EffectiveAssistant:    "林妹妹",
				ChatAssistantOverride: "林妹妹",
				NameSource:            "Current chat name",
				OverridesGlobal:       true,
				PersonaLabel:          "Creative",
				WorkspacePath:         "/repo",
				KnownUsers: []KnownUserView{{
					UserID: "alice",
					Label:  "Alice Chen",
				}},
				HistoryTotalCount: 1,
				History: []ChatSessionView{{
					SessionID:    "wecom:dm:alice:171",
					LastActivity: time.Unix(1700000000, 0),
					Visible:      true,
				}},
			}},
		},
		detail: ChatView{
			BaseSessionID:         "wecom:dm:alice",
			DisplayLabel:          "Direct Message / alice",
			KindLabel:             "Direct message",
			CurrentSessionID:      "wecom:dm:alice:171",
			RecallSessionID:       "wecom:dm:alice",
			LastActivity:          time.Unix(1700000000, 0),
			Epoch:                 171,
			EffectiveAssistant:    "林妹妹",
			ChatAssistantOverride: "林妹妹",
			NameSource:            "Current chat name",
			OverridesGlobal:       true,
			PersonaLabel:          "Creative",
			WorkspacePath:         "/repo",
			KnownUsers: []KnownUserView{{
				UserID: "alice",
				Label:  "Alice Chen",
			}},
			HistoryTotalCount: 2,
			HistoryTruncated:  true,
			History: []ChatSessionView{{
				SessionID:    "wecom:dm:alice:171",
				LastActivity: time.Unix(1700000000, 0),
				Visible:      true,
			}, {
				SessionID:    "wecom:dm:alice:170",
				LastActivity: time.Unix(1699999990, 0),
				Visible:      false,
			}},
		},
		history: ChatHistoryPage{
			BaseSessionID:     "wecom:dm:alice",
			SessionLineCount:  2,
			TurnCount:         3,
			ReturnedTurnCount: 2,
			NextCursor:        "2",
			Bounded:           true,
			Items: []ChatHistoryItem{{
				Kind:         chatHistoryItemKindSession,
				SessionID:    "wecom:dm:alice:171",
				SessionLabel: "Current session",
				LastActivity: time.Unix(1700000000, 0),
				Current:      true,
			}, {
				Kind:      chatHistoryItemKindTurn,
				Role:      "assistant",
				Speaker:   "林妹妹",
				Text:      "I am using this chat's current name.",
				Timestamp: time.Unix(1700000010, 0),
			}, {
				Kind:      chatHistoryItemKindTurn,
				Role:      "assistant",
				Speaker:   "林妹妹",
				Text:      "Older session reply.",
				Timestamp: time.Unix(1699999990, 0),
			}},
		},
	}
	identity := &stubIdentityProvider{
		status: IdentityStatus{
			Enabled:        true,
			EffectiveName:  "Claw",
			RuntimeProduct: "trpc-claw",
		},
	}
	svc := New(Config{
		Chats:    chats,
		Identity: identity,
	})

	req := httptest.NewRequest(
		http.MethodGet,
		routeChats+"?chat_id=wecom%3Adm%3Aalice",
		nil,
	)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Tracked Chats")
	require.Contains(t, body, "Selected Chat")
	require.Contains(t, body, "Direct Message / alice")
	require.Contains(t, body, "Current chat name")
	require.Contains(t, body, "Overview")
	require.Contains(t, body, "History")
	require.Contains(t, body, "Actions")
	require.Contains(t, body, "<code>wecom:dm:alice</code>")
	require.Contains(t, body, "href=\"identity#identity-global\"")
	require.Contains(t, body, "Alice Chen (alice)")
	require.Contains(t, body, "data-chat-history-root")
	require.Contains(
		t,
		body,
		`data-chat-history-path="api/chats/history"`,
	)
	require.Contains(t, body, "window.location.href")
	require.NotContains(t, body, "window.location.origin")
	require.Contains(
		t,
		body,
		"Expand this panel to load the newest visible",
	)
	require.Contains(t, body, "Show 1 older tracked sessions")
	require.Contains(
		t,
		body,
		"Showing the most recent tracked sessions in this admin view.",
	)
	require.Equal(t, "wecom:dm:alice", chats.detailChatID)
	require.Equal(t, 1, chats.detailCount)

	req = httptest.NewRequest(
		http.MethodGet,
		routeChatHistoryJSON+"?chat_id=wecom%3Adm%3Aalice",
		nil,
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(
		t,
		rec.Body.String(),
		"\"text\": \"I am using this chat's current name.\"",
	)
	require.Contains(t, rec.Body.String(), "\"next_cursor\": \"2\"")
	require.Equal(t, "wecom:dm:alice", chats.historyChatID)
	require.Empty(t, chats.historyCursor)
	require.Equal(t, 1, chats.historyCount)

	req = httptest.NewRequest(http.MethodGet, routeOverview, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, chats.detailCount)

	req = httptest.NewRequest(http.MethodGet, routeChatsJSON, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "\"override_count\": 1")
	require.Contains(
		t,
		rec.Body.String(),
		"\"base_session_id\": \"wecom:dm:alice\"",
	)
}

func TestService_ChatsPageFallbackStates(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	req := httptest.NewRequest(http.MethodGet, routeChats, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(
		t,
		rec.Body.String(),
		"Chat tracking is not available for this runtime.",
	)

	chats := &stubChatsProvider{
		status: ChatsStatus{
			Enabled: true,
		},
	}
	svc = New(Config{Chats: chats})

	req = httptest.NewRequest(
		http.MethodGet,
		routeChats+"?chat_id=missing",
		nil,
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(
		t,
		rec.Body.String(),
		"No tracked chats are available yet.",
	)
	require.Contains(
		t,
		rec.Body.String(),
		"Choose a tracked chat from the list to inspect its current",
	)

	req = httptest.NewRequest(http.MethodPost, routeChatsJSON, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	svc = New(Config{
		Chats: &stubStatusOnlyChatsProvider{
			status: ChatsStatus{Enabled: true},
		},
	})
	req = httptest.NewRequest(
		http.MethodGet,
		routeChatHistoryJSON+"?chat_id=missing",
		nil,
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestChatsHelpers(t *testing.T) {
	t.Parallel()

	require.Empty(t, selectedChatID(nil))

	req := httptest.NewRequest(
		http.MethodGet,
		routeChats+"?chat_id=%20wecom%3Adm%3Abob%20",
		nil,
	)
	require.Equal(t, "wecom:dm:bob", selectedChatID(req))

	status := ChatsStatus{
		Chats: []ChatView{
			{
				BaseSessionID: "wecom:dm:alice",
				LastActivity:  time.Unix(1700000000, 0),
			},
			{
				BaseSessionID:         "wecom:group:room-1",
				DisplayLabel:          "Room 1",
				ChatAssistantOverride: "林妹妹",
				OverridesGlobal:       true,
				LastActivity:          time.Unix(1700000100, 0),
				KnownUserIDs:          []string{"alice", "bob"},
			},
			{
				BaseSessionID:   "wecom:dm:bob",
				OverridesGlobal: true,
				LastActivity:    time.Unix(1700000050, 0),
				NameSource:      "Custom source",
			},
		},
	}

	selected := selectChatView(status, "")
	require.NotNil(t, selected)
	require.Equal(t, "wecom:dm:alice", selected.BaseSessionID)

	selected = selectChatView(status, "wecom:dm:bob")
	require.NotNil(t, selected)
	require.Equal(t, "wecom:dm:bob", selected.BaseSessionID)

	require.Nil(t, selectChatView(status, "missing"))
	require.Nil(t, selectChatView(ChatsStatus{}, ""))

	require.Equal(
		t,
		"wecom:dm:alice",
		chatDisplayLabel(ChatView{BaseSessionID: "wecom:dm:alice"}),
	)
	require.Equal(
		t,
		"No tracked sessions are currently available.",
		chatHistorySummary(ChatView{}),
	)
	require.Equal(
		t,
		"2 tracked session lines",
		chatHistorySummary(ChatView{HistoryTotalCount: 2}),
	)
	require.Equal(t, "-", chatKnownUsers(ChatView{}))
	require.Equal(t, "alice, bob", chatKnownUsers(status.Chats[1]))
	require.Equal(
		t,
		"Alice (T00010001), Bob (T00010002)",
		chatKnownUsers(ChatView{
			KnownUsers: []KnownUserView{
				{
					UserID: "T00010001",
					Label:  "Alice",
				},
				{
					UserID: "T00010002",
					Label:  "Bob",
				},
			},
		}),
	)
	require.Equal(
		t,
		"T00010003",
		chatKnownUsers(ChatView{
			KnownUsers: []KnownUserView{{
				UserID: "T00010003",
			}},
		}),
	)
	require.Equal(
		t,
		"Current chat name",
		chatNameSourceLabel(ChatView{
			ChatAssistantOverride: "林妹妹",
		}),
	)
	require.Equal(
		t,
		"Custom source",
		chatNameSourceLabel(status.Chats[2]),
	)
	require.Equal(t, "Default name", chatNameSourceLabel(ChatView{}))
	require.False(t, chatHasTranscript(ChatView{}))
	require.True(t, chatHasTranscript(ChatView{
		Transcript: []ChatTranscriptView{{
			SessionID: "wecom:dm:alice:171",
		}},
	}))
	require.Equal(
		t,
		"No recent transcript is currently available.",
		chatTranscriptSummary(ChatView{}),
	)
	require.Equal(
		t,
		"1 recent session lines · 2 visible turns",
		chatTranscriptSummary(ChatView{
			Transcript: []ChatTranscriptView{{
				Turns: []ChatTurnView{{}, {}},
			}},
		}),
	)
	require.Equal(
		t,
		"Current session",
		chatTranscriptLabel(ChatTranscriptView{Current: true}),
	)
	require.Equal(
		t,
		"Recall session",
		chatTranscriptLabel(ChatTranscriptView{Recall: true}),
	)
	require.Equal(
		t,
		"Recent session",
		chatTranscriptLabel(ChatTranscriptView{}),
	)
	require.Equal(
		t,
		"Alice Chen",
		chatTurnSpeaker(ChatTurnView{Speaker: "Alice Chen"}),
	)
	require.Equal(
		t,
		"User",
		chatTurnSpeaker(ChatTurnView{Role: "user"}),
	)
	require.Equal(
		t,
		"Assistant",
		chatTurnSpeaker(ChatTurnView{Role: "assistant"}),
	)
	require.Equal(
		t,
		"System",
		chatTurnSpeaker(ChatTurnView{Role: "system"}),
	)
	require.Equal(
		t,
		"Turn",
		chatTurnSpeaker(ChatTurnView{Role: "tool"}),
	)
	require.Equal(
		t,
		"Alice Chen",
		chatKnownUserLabel(KnownUserView{Label: "Alice Chen"}),
	)
	require.Len(
		t,
		chatVisibleHistory(ChatView{
			History: []ChatSessionView{{Visible: true}, {Visible: false}},
		}),
		1,
	)
	require.Len(
		t,
		chatHiddenHistory(ChatView{
			History: []ChatSessionView{{Visible: true}, {Visible: false}},
		}),
		1,
	)
	require.Len(
		t,
		chatVisibleTranscript(ChatView{
			Transcript: []ChatTranscriptView{
				{Visible: true}, {Visible: false},
			},
		}),
		1,
	)
	require.Len(
		t,
		chatHiddenTranscript(ChatView{
			Transcript: []ChatTranscriptView{
				{Visible: true}, {Visible: false},
			},
		}),
		1,
	)
	require.Len(
		t,
		chatVisibleTurns(ChatTranscriptView{
			Turns: []ChatTurnView{{Visible: true}, {Visible: false}},
		}),
		1,
	)
	require.Len(
		t,
		chatHiddenTurns(ChatTranscriptView{
			Turns: []ChatTurnView{{Visible: true}, {Visible: false}},
		}),
		1,
	)
	require.False(t, hasTime(time.Time{}))
	require.True(t, hasTime(time.Unix(1700000000, 0)))

	sample := chatOverrideSample(status, 1)
	require.Len(t, sample, 1)
	require.Equal(t, "wecom:group:room-1", sample[0].BaseSessionID)

	sample = chatOverrideSample(status, 0)
	require.Len(t, sample, 2)

	sample = chatOverrideSample(status, 5)
	require.Len(t, sample, 2)

	require.Equal(t, 2, chatOverrideCount(status.Chats))

	chats := &stubChatsProvider{
		status: status,
		detail: ChatView{
			Transcript: []ChatTranscriptView{{
				SessionID: "wecom:dm:bob:2",
			}},
		},
		detailRaw: true,
	}
	selected, detailErr := resolveSelectedChat(
		status,
		chats,
		"wecom:dm:bob",
	)
	require.NotNil(t, selected)
	require.Empty(t, detailErr)
	require.Len(t, selected.Transcript, 1)
	require.Equal(t, "Custom source", selected.NameSource)
	require.True(t, selected.OverridesGlobal)
	require.Equal(t, "wecom:dm:bob", chats.detailChatID)

	chats.detail = ChatView{
		BaseSessionID: "wrong",
		Transcript: []ChatTranscriptView{{
			SessionID: "wrong:2",
		}},
	}
	chats.detailRaw = true
	selected, detailErr = resolveSelectedChat(
		status,
		chats,
		"wecom:dm:bob",
	)
	require.NotNil(t, selected)
	require.Equal(
		t,
		"chat detail mismatch: expected \"wecom:dm:bob\", got \"wrong\"",
		detailErr,
	)
	require.Nil(t, selected.Transcript)
	require.Equal(t, "wecom:dm:bob", selected.BaseSessionID)
	require.Equal(t, "Custom source", selected.NameSource)

	selected, detailErr = resolveSelectedChat(
		status,
		&stubStatusOnlyChatsProvider{status: status},
		"wecom:dm:bob",
	)
	require.NotNil(t, selected)
	require.Empty(t, detailErr)
	require.Nil(t, selected.Transcript)

	chats.detailErr = errors.New("boom")
	selected, detailErr = resolveSelectedChat(
		status,
		chats,
		"wecom:dm:bob",
	)
	require.NotNil(t, selected)
	require.Equal(t, "boom", detailErr)

	merged := mergeChatView(
		ChatView{
			BaseSessionID:       "wecom:dm:bob",
			DisplayLabel:        "Bob",
			NameSource:          "Current chat name",
			OverridesGlobal:     true,
			HistoryTotalCount:   2,
			TranscriptTruncated: true,
		},
		ChatView{
			HistoryTotalCount: 3,
			HistoryTruncated:  true,
			Transcript: []ChatTranscriptView{{
				SessionID: "wecom:dm:bob:2",
			}},
		},
	)
	require.Equal(t, "Bob", merged.DisplayLabel)
	require.Equal(t, "Current chat name", merged.NameSource)
	require.True(t, merged.OverridesGlobal)
	require.Equal(t, 3, merged.HistoryTotalCount)
	require.True(t, merged.HistoryTruncated)
	require.True(t, merged.TranscriptTruncated)
	require.Len(t, merged.Transcript, 1)

	merged = mergeChatView(
		ChatView{BaseSessionID: "wecom:dm:alice"},
		ChatView{
			BaseSessionID:    "wecom:dm:alice",
			DisplayLabel:     "Alice",
			Kind:             "dm",
			KindLabel:        "Direct message",
			CurrentSessionID: "sess-2",
			RecallSessionID:  "sess-1",
		},
	)
	require.Equal(t, "Alice", merged.DisplayLabel)
	require.Equal(t, "dm", merged.Kind)
	require.Equal(t, "Direct message", merged.KindLabel)
	require.Equal(t, "sess-2", merged.CurrentSessionID)
	require.Equal(t, "sess-1", merged.RecallSessionID)

	selected, detailErr = resolveSelectedChat(
		status,
		chats,
		"missing",
	)
	require.Nil(t, selected)
	require.Empty(t, detailErr)
}

func TestService_ChatsStatusError(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Chats: &stubChatsProvider{err: errors.New("boom")},
	})
	status := svc.chatsStatus()
	require.True(t, status.Enabled)
	require.Equal(t, "boom", status.Error)
}

func TestService_PersonasPageAndActions(t *testing.T) {
	t.Parallel()

	personas := &stubPersonasProvider{
		status: PersonasStatus{
			Enabled:          true,
			DefaultPersonaID: "friendly",
			DefaultOptions: []PersonaOption{{
				ID:   "friendly",
				Name: "Friendly",
			}},
			Stores: []PersonaStoreView{{
				Key:           "agent",
				Title:         "Shared Persona Store",
				UsageLabels:   []string{"Agent Personas", "WeCom Personas 1"},
				CreateEnabled: true,
				Personas: []PersonaView{
					{
						ID:       "warm",
						Name:     "Warm",
						Summary:  "A direct custom tone.",
						Prompt:   "custom tone",
						Editable: true,
					},
					{
						ID:      "friendly",
						Name:    "Friendly",
						Summary: "Warm and approachable.",
						Prompt:  "warm tone",
						BuiltIn: true,
					},
				},
			}},
		},
	}
	svc := New(Config{Personas: personas})

	req := httptest.NewRequest(http.MethodGet, routePersonas, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Persona Management")
	require.Contains(t, rec.Body.String(), "Shared Persona Store")
	require.Contains(t, rec.Body.String(), "Agent Personas")
	require.Contains(t, rec.Body.String(), "WeCom Personas 1")
	require.Contains(t, rec.Body.String(), "Create Persona")
	require.Contains(t, rec.Body.String(), "Custom Personas")
	require.Contains(t, rec.Body.String(), "Built-in Personas")
	require.Contains(t, rec.Body.String(), "/api/personas")
	require.Less(
		t,
		strings.Index(rec.Body.String(), "Create Persona"),
		strings.Index(rec.Body.String(), "Custom Personas"),
	)
}

func TestService_PromptAndPersonaJSONAndMutations(t *testing.T) {
	t.Parallel()

	prompts := &stubPromptsProvider{
		status: PromptsStatus{
			Enabled: true,
			Bundles: []PromptBundleState{{
				Key:   "agent_instruction",
				Title: "Instruction",
			}},
		},
	}
	personas := &stubPersonasProvider{
		status: PersonasStatus{
			Enabled: true,
			Stores: []PersonaStoreView{{
				Key:   "agent",
				Title: "Agent Personas",
			}},
		},
	}
	svc := New(Config{
		Prompts:  prompts,
		Personas: personas,
	})

	req := httptest.NewRequest(http.MethodGet, routePromptsJSON, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "agent_instruction")

	req = httptest.NewRequest(http.MethodGet, routePersonasJSON, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Agent Personas")

	values := url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptPath:      {"/tmp/instruction.md"},
		formPromptContent:   {"file body"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, prompts.fileCount)
	require.Equal(t, "/tmp/instruction.md", prompts.filePath)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptFileName:  {"20_extra.md"},
		formPromptContent:   {"extra"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileCreate,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, prompts.createCount)
	require.Equal(t, "20_extra.md", prompts.createName)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptPath:      {"/tmp/instruction.md"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileDelete,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, prompts.deleteCount)
	require.Equal(t, "/tmp/instruction.md", prompts.deletePath)

	values = url.Values{
		formPersonaStoreKey: {"agent"},
		formPersonaID:       {"friendly"},
		formReturnPath:      {routePersonas},
		formReturnTo:        {"persona-store-agent"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaDelete,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, personas.deleteCount)
	require.Equal(t, "friendly", personas.deleteID)
}

func TestService_PromptAndPersonaActionErrors(t *testing.T) {
	t.Parallel()

	prompts := &stubPromptsProvider{
		inlineErr:  errors.New("inline failed"),
		runtimeErr: errors.New("runtime failed"),
		fileErr:    errors.New("file failed"),
		createErr:  errors.New("create failed"),
		deleteErr:  errors.New("delete failed"),
	}
	personas := &stubPersonasProvider{
		saveErr:    errors.New("save failed"),
		deleteErr:  errors.New("delete failed"),
		defaultErr: errors.New("default failed"),
	}
	identity := &stubIdentityProvider{
		saveErr: errors.New("save failed"),
	}
	svc := New(Config{
		Prompts:  prompts,
		Identity: identity,
		Personas: personas,
	})

	req := httptest.NewRequest(http.MethodPost, routePromptsJSON, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	req = httptest.NewRequest(http.MethodPost, routePersonasJSON, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	req = httptest.NewRequest(http.MethodPost, routeIdentityJSON, nil)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	values := url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptContent:   {"inline"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptInlineSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"inline+failed",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptContent:   {"runtime"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptRuntimeSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"runtime+failed",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"path+is+required",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptPath:      {"/tmp/instruction.md"},
		formPromptContent:   {"body"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"file+failed",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptContent:   {"body"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileCreate,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"file_name+is+required",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptFileName:  {"20_extra.md"},
		formPromptContent:   {"body"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileCreate,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"create+failed",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileDelete,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"path+is+required",
	)

	values = url.Values{
		formPromptBundleKey: {"agent_instruction"},
		formPromptPath:      {"/tmp/instruction.md"},
		formReturnPath:      {routePrompts},
		formReturnTo:        {"prompt-agent_instruction"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptFileDelete,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"delete+failed",
	)

	values = url.Values{
		formReturnPath: {routePrompts},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePromptInlineSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"bundle_key+is+required",
	)

	values = url.Values{
		formAssistantName: {"Claw"},
		formReturnPath:    {routeIdentity},
		formReturnTo:      {"identity-global"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routeIdentitySave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"save+failed",
	)

	values = url.Values{
		formPersonaStoreKey: {"agent"},
		formPersonaID:       {"friendly"},
		formPersonaName:     {"Friendly"},
		formPersonaPrompt:   {"prompt"},
		formReturnPath:      {routePersonas},
		formReturnTo:        {"persona-store-agent"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"save+failed",
	)

	values = url.Values{
		formPersonaStoreKey: {"agent"},
		formReturnPath:      {routePersonas},
		formReturnTo:        {"persona-store-agent"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaDelete,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"persona_id+is+required",
	)

	values = url.Values{
		formPersonaStoreKey: {"agent"},
		formPersonaID:       {"friendly"},
		formReturnPath:      {routePersonas},
		formReturnTo:        {"persona-store-agent"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaDelete,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"delete+failed",
	)

	values = url.Values{
		formPersonaID:  {"friendly"},
		formReturnPath: {routePersonas},
		formReturnTo:   {"personas-default"},
	}
	req = httptest.NewRequest(
		http.MethodPost,
		routePersonaDefaultSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(
		t,
		rec.Header().Get(headerLocation),
		"default+failed",
	)

	nilSvc := New(Config{})
	req = httptest.NewRequest(http.MethodPost, routePromptInlineSave, nil)
	rec = httptest.NewRecorder()
	nilSvc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodPost, routePersonaSave, nil)
	rec = httptest.NewRecorder()
	nilSvc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodPost, routeIdentitySave, nil)
	rec = httptest.NewRecorder()
	nilSvc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodPost, routePersonaDefaultSave, nil)
	rec = httptest.NewRecorder()
	nilSvc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPersonaHelperFunctions(t *testing.T) {
	t.Parallel()

	store := PersonaStoreView{
		UsageLabels: []string{
			"Agent Personas",
			" ",
			"WeCom Personas 1",
			"Agent Personas",
		},
		Personas: []PersonaView{
			{
				ID:      "friendly",
				Name:    "Friendly",
				Summary: "Warm and approachable.",
				Prompt:  "Friendly prompt.",
				BuiltIn: true,
			},
			{
				ID:       "custom",
				Prompt:   "Custom persona prompt.",
				Editable: true,
			},
		},
	}

	require.Equal(
		t,
		personaStoreTitleFallback,
		personaStoreTitle(PersonaStoreView{}),
	)
	require.Equal(
		t,
		[]string{"Agent Personas", "WeCom Personas 1"},
		personaStoreUsageLabels(store),
	)
	require.Len(t, personaCustomPersonas(store), 1)
	require.Len(t, personaBuiltInPersonas(store), 1)
	require.Equal(t, 1, personaStoreCustomCount(store))
	require.Equal(t, 1, personaStoreBuiltInCount(store))
	require.Equal(t, "custom", personaDisplayName(store.Personas[1]))
	require.Equal(t, personaKindBuiltIn, personaKindLabel(store.Personas[0]))
	require.Equal(t, personaKindCustom, personaKindLabel(store.Personas[1]))
	require.Contains(
		t,
		personaSummaryText(store.Personas[1]),
		"Starts with: Custom persona prompt.",
	)
}

func TestPromptStatusAndFormParsingErrors(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Prompts: &stubPromptsProvider{
			err: errors.New("prompt status failed"),
		},
		Identity: &stubIdentityProvider{
			err: errors.New("identity status failed"),
		},
		Personas: &stubPersonasProvider{
			err: errors.New("persona status failed"),
		},
	})

	require.Equal(
		t,
		"prompt status failed",
		svc.promptsStatus().Error,
	)
	require.Equal(
		t,
		"persona status failed",
		svc.personasStatus().Error,
	)
	require.Equal(
		t,
		"identity status failed",
		svc.identityStatus().Error,
	)

	badForm := "%"
	contentType := "application/x-www-form-urlencoded"

	promptReq := httptest.NewRequest(
		http.MethodPost,
		routePromptRuntimeSave,
		strings.NewReader(badForm),
	)
	promptReq.Header.Set("Content-Type", contentType)
	promptRec := httptest.NewRecorder()
	_, _, _, ok := svc.requirePromptPOST(promptRec, promptReq)
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, promptRec.Code)

	personaReq := httptest.NewRequest(
		http.MethodPost,
		routePersonaSave,
		strings.NewReader(badForm),
	)
	personaReq.Header.Set("Content-Type", contentType)
	personaRec := httptest.NewRecorder()
	_, _, _, _, _, ok = svc.requirePersonaPOST(personaRec, personaReq)
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, personaRec.Code)

	req := httptest.NewRequest(
		http.MethodPost,
		routePersonaDefaultSave,
		strings.NewReader(badForm),
	)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = httptest.NewRequest(
		http.MethodPost,
		routeIdentitySave,
		strings.NewReader(badForm),
	)
	req.Header.Set("Content-Type", contentType)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPromptAndPersonaHandlersRejectWrongMethod(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Prompts:  &stubPromptsProvider{},
		Identity: &stubIdentityProvider{},
		Personas: &stubPersonasProvider{},
	})

	paths := []string{
		routePromptInlineSave,
		routePromptRuntimeSave,
		routePromptFileSave,
		routePromptFileCreate,
		routePromptFileDelete,
		routeIdentitySave,
		routePersonaSave,
		routePersonaDelete,
		routePersonaDefaultSave,
	}

	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		svc.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestSkillInstallViewsFromStatus_TrimsValues(t *testing.T) {
	t.Parallel()

	out := skillInstallViewsFromStatus([]ocskills.StatusInstallOption{
		{
			ID:    " brew-jq ",
			Kind:  " brew ",
			Label: " brew install jq ",
			Bins:  []string{" jq ", " yq "},
		},
		{
			ID:    " custom ",
			Kind:  " custom ",
			Label: " custom installer ",
		},
	})
	require.Equal(
		t,
		[]skillInstallView{
			{
				ID:    "brew-jq",
				Kind:  "brew",
				Label: "brew install jq",
				Bins:  []string{" jq ", " yq "},
			},
			{
				ID:    "custom",
				Kind:  "custom",
				Label: "custom installer",
			},
		},
		out,
	)
	require.Nil(t, skillInstallViewsFromStatus(nil))
}

func TestServiceToggleSkillEndpointRequiresConfigPath(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillToggle,
		strings.NewReader("skill_key=weather-api&enabled=true"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Zero(t, provider.setCount)
	values := url.Values{}
	values.Set(
		queryError,
		"skill toggles require a config-backed runtime",
	)
	target := (&url.URL{
		Path:     "../..",
		RawQuery: values.Encode(),
	}).String()
	require.Equal(
		t,
		target,
		rr.Header().Get(headerLocation),
	)
}

func TestServiceJobEndpoints(t *testing.T) {
	t.Parallel()

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	job, err := cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  "u1",
	})
	require.NoError(t, err)

	svc := New(Config{Cron: cronSvc})
	handler := svc.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeJobsJSON, nil)
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), job.ID)

	runReq := httptest.NewRequest(
		http.MethodPost,
		routeJobRun,
		strings.NewReader("job_id="+job.ID),
	)
	runReq.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	runRR := httptest.NewRecorder()
	handler.ServeHTTP(runRR, runReq)
	require.Equal(t, http.StatusSeeOther, runRR.Code)

	removeReq := httptest.NewRequest(
		http.MethodPost,
		routeJobRemove,
		strings.NewReader("job_id="+job.ID),
	)
	removeReq.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	removeRR := httptest.NewRecorder()
	handler.ServeHTTP(removeRR, removeReq)
	require.Equal(t, http.StatusSeeOther, removeRR.Code)
	require.Nil(t, cronSvc.Get(job.ID))
}

func TestBrowserEndpointSummary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		view browserEndpointView
		want string
	}{
		{
			name: "empty",
			want: "-",
		},
		{
			name: "down with error",
			view: browserEndpointView{
				URL:   "http://127.0.0.1:19790",
				Error: "connection refused",
			},
			want: "down: connection refused",
		},
		{
			name: "down",
			view: browserEndpointView{
				URL: "http://127.0.0.1:19790",
			},
			want: "down",
		},
		{
			name: "reachable without profiles",
			view: browserEndpointView{
				URL:       "http://127.0.0.1:19790",
				Reachable: true,
			},
			want: "reachable",
		},
		{
			name: "reachable with profiles",
			view: browserEndpointView{
				URL:       "http://127.0.0.1:19790",
				Reachable: true,
				Profiles: []browserRemoteProbe{
					{Name: "chrome", State: "ready"},
					{Name: "", State: "busy"},
					{Name: "edge"},
					{},
				},
			},
			want: "chrome=ready, busy, edge",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, browserEndpointSummary(tc.view))
		})
	}
}

func TestServiceProbeBrowserEndpoint_CachesAndHandlesErrors(
	t *testing.T,
) {
	t.Parallel()

	t.Run("caches reachable result", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			requestCount++
			require.Equal(t, "/profiles", r.URL.Path)
			writeJSON(w, http.StatusOK, map[string]any{
				"profiles": []map[string]any{{
					"name": "chrome",
				}, {
					"name": "alpha",
				}},
			})
		}))
		t.Cleanup(server.Close)

		svc := New(Config{})
		cache := make(map[string]browserEndpointView)

		view := svc.probeBrowserEndpoint(server.URL, cache)
		require.True(t, view.Reachable)
		require.Len(t, view.Profiles, 2)
		require.Equal(t, "alpha", view.Profiles[0].Name)
		require.Equal(t, "chrome", view.Profiles[1].Name)

		cached := svc.probeBrowserEndpoint(server.URL, cache)
		require.Equal(t, view, cached)
		require.Equal(t, 1, requestCount)
	})

	t.Run("bad status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		}))
		t.Cleanup(server.Close)

		view := New(Config{}).probeBrowserEndpoint(
			server.URL,
			make(map[string]browserEndpointView),
		)
		require.False(t, view.Reachable)
		require.Contains(t, view.Error, "unexpected status")
	})

	t.Run("bad json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			_, _ = w.Write([]byte("{"))
		}))
		t.Cleanup(server.Close)

		view := New(Config{}).probeBrowserEndpoint(
			server.URL,
			make(map[string]browserEndpointView),
		)
		require.False(t, view.Reachable)
		require.Contains(t, view.Error, "decode profiles")
	})
}

func TestResolveDebugRootFile_ErrorPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	serviceDir := filepath.Join(root, "services")
	require.NoError(t, os.MkdirAll(serviceDir, 0o755))

	logPath := filepath.Join(serviceDir, "browser-server.log")
	require.NoError(t, os.WriteFile(logPath, []byte("ready\n"), 0o600))

	got, err := resolveDebugRootFile(root, "services/browser-server.log")
	require.NoError(t, err)
	require.Equal(t, logPath, got)

	_, err = resolveDebugRootFile(root, ".")
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")

	_, err = resolveDebugRootFile(root, "/tmp/browser-server.log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid debug path")

	_, err = resolveDebugRootFile(root, "services/missing.log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	_, err = resolveDebugRootFile(root, "services")
	require.Error(t, err)
	require.Contains(t, err.Error(), "directory")
}

func TestServiceSnapshotIncludesCronSummary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
		cron.WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	_, err = cronSvc.Add(&cron.Job{
		Name:    "report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "5m",
		},
		Message: "collect cpu and mem",
		UserID:  "u1",
	})
	require.NoError(t, err)

	svc := New(
		Config{Cron: cronSvc},
		WithClock(func() time.Time { return now }),
	)
	snap := svc.Snapshot()
	require.True(t, snap.Cron.Enabled)
	require.Equal(t, 1, snap.Cron.JobCount)
	require.Len(t, snap.Cron.Jobs, 1)
	require.Equal(t, "every 5m", snap.Cron.Jobs[0].Schedule)
}

func TestServiceSnapshotIncludesBrowserSummary(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(1700000000, 0)

	svc := New(Config{
		Browser: BrowserConfig{
			Managed: stubBMP{
				status: BrowserManagedService{
					Enabled:         true,
					Managed:         true,
					State:           "running",
					URL:             "http://127.0.0.1:19790",
					PID:             4321,
					LogPath:         "/tmp/debug/services/browser-server.log",
					LogRelativePath: "services/browser-server.log",
					StartedAt:       &startedAt,
					RecentLogs: []string{
						"OpenClaw browser server listening",
					},
				},
			},
			Providers: []BrowserProvider{{
				Name:             "primary",
				DefaultProfile:   "openclaw",
				HostServerURL:    "http://127.0.0.1:19790",
				SandboxServerURL: "http://127.0.0.1:20790",
				AllowLoopback:    true,
				Profiles: []BrowserProfile{{
					Name:      "openclaw",
					Transport: "stdio",
				}, {
					Name:             "chrome",
					BrowserServerURL: "http://127.0.0.1:19790",
				}},
				Nodes: []BrowserNode{{
					ID:        "edge",
					ServerURL: "http://node.example:7777",
				}},
			}},
		},
	})

	snap := svc.Snapshot()
	require.True(t, snap.Browser.Enabled)
	require.Equal(t, 1, snap.Browser.ProviderCount)
	require.Equal(t, 2, snap.Browser.ProfileCount)
	require.Equal(t, 1, snap.Browser.NodeCount)
	require.Len(t, snap.Browser.Providers, 1)
	require.Equal(t, "primary", snap.Browser.Providers[0].Name)
	require.Equal(
		t,
		"http://127.0.0.1:19790",
		snap.Browser.Providers[0].HostServerURL,
	)
	require.True(t, snap.Browser.Managed.Enabled)
	require.True(t, snap.Browser.Managed.Managed)
	require.Equal(t, "running", snap.Browser.Managed.State)
	require.Equal(
		t,
		"/debug/file?path=services%2Fbrowser-server.log",
		snap.Browser.Managed.LogURL,
	)
	require.Len(t, snap.Browser.Managed.RecentLogs, 1)
}

func TestServiceSnapshotProbesBrowserEndpoints(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, "/profiles", r.URL.Path)
		writeJSON(w, http.StatusOK, map[string]any{
			"profiles": []map[string]any{{
				"name":   "openclaw",
				"state":  "ready",
				"driver": "playwright",
				"tabs":   1,
			}},
		})
	}))
	t.Cleanup(server.Close)

	svc := New(
		Config{
			Browser: BrowserConfig{
				Providers: []BrowserProvider{{
					Name:           "primary",
					DefaultProfile: "openclaw",
					HostServerURL:  server.URL,
					Nodes: []BrowserNode{{
						ID:        "edge",
						ServerURL: server.URL,
					}},
				}},
			},
		},
		WithBrowserHTTPClient(server.Client()),
	)

	snap := svc.Snapshot()
	require.True(t, snap.Browser.Enabled)
	require.Len(t, snap.Browser.Providers, 1)
	require.True(t, snap.Browser.Providers[0].Host.Reachable)
	require.Len(t, snap.Browser.Providers[0].Host.Profiles, 1)
	require.Equal(
		t,
		"openclaw",
		snap.Browser.Providers[0].Host.Profiles[0].Name,
	)
	require.Len(t, snap.Browser.Providers[0].Nodes, 1)
	require.True(t, snap.Browser.Providers[0].Nodes[0].Status.Reachable)
}

func TestServiceSnapshotIncludesUploadSourceCounts(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.SaveWithInfo(
		context.Background(),
		scope,
		"voice.ogg",
		uploads.FileMetadata{
			MimeType: "audio/ogg",
			Source:   uploads.SourceInbound,
		},
		[]byte("voice"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithInfo(
		context.Background(),
		scope,
		"page-1.pdf",
		uploads.FileMetadata{
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	snap := New(Config{StateDir: stateDir}).Snapshot()
	require.True(t, snap.Uploads.Enabled)
	require.Len(t, snap.Uploads.SourceCounts, 2)
	require.Equal(t, "derived", snap.Uploads.SourceCounts[0].Source)
	require.Equal(t, 1, snap.Uploads.SourceCounts[0].Count)
	require.Equal(t, "inbound", snap.Uploads.SourceCounts[1].Source)
	require.Equal(t, 1, snap.Uploads.SourceCounts[1].Count)
}

func TestServiceDebugEndpoints(t *testing.T) {
	t.Parallel()

	debugRoot := t.TempDir()
	now := time.Date(2026, 3, 6, 18, 10, 0, 0, time.UTC)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:1",
		"req-1",
		now,
		"trace-1",
	)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:2",
		"req-2",
		now.Add(-time.Minute),
		"trace-2",
	)

	svc := New(
		Config{
			AppName:        "openclaw",
			InstanceID:     "inst-1",
			StartedAt:      now.Add(-time.Hour),
			Hostname:       "host-1",
			PID:            4321,
			GoVersion:      "go1.test",
			AgentType:      "llm",
			ModelMode:      "openai",
			ModelName:      "gpt-5",
			SessionBackend: "sqlite",
			MemoryBackend:  "inmemory",
			DebugDir:       debugRoot,
			Langfuse: LangfuseStatus{
				Enabled:   true,
				Ready:     true,
				UIBaseURL: "http://127.0.0.1:3000",
				TraceURLTemplate: "http://127.0.0.1:3000/project/" +
					"local-dev/traces/{{trace_id}}",
			},
		},
		WithClock(func() time.Time { return now }),
	)
	handler := svc.Handler()

	snap := svc.Snapshot()
	require.True(t, snap.Debug.Enabled)
	require.Equal(t, 2, snap.Debug.SessionCount)
	require.Equal(t, 2, snap.Debug.TraceCount)
	require.Len(t, snap.Debug.Sessions, 2)
	require.Len(t, snap.Debug.RecentTraces, 2)
	require.True(t, snap.Langfuse.Enabled)
	require.True(t, snap.Langfuse.Ready)
	require.Equal(
		t,
		"trace-1",
		snap.Debug.RecentTraces[0].TraceID,
	)
	require.Equal(
		t,
		"http://127.0.0.1:3000/project/local-dev/traces/trace-1",
		snap.Debug.RecentTraces[0].LangfuseURL,
	)

	sessionsRR := httptest.NewRecorder()
	sessionsReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugSessionsJSON,
		nil,
	)
	handler.ServeHTTP(sessionsRR, sessionsReq)
	require.Equal(t, http.StatusOK, sessionsRR.Code)
	require.Contains(t, sessionsRR.Body.String(), "telegram:dm:1")
	require.Contains(t, sessionsRR.Body.String(), "trace-1")

	traceQuery := routeDebugTracesJSON + "?" +
		querySessionID + "=" +
		url.QueryEscape("telegram:dm:1")
	tracesRR := httptest.NewRecorder()
	tracesReq := httptest.NewRequest(http.MethodGet, traceQuery, nil)
	handler.ServeHTTP(tracesRR, tracesReq)
	require.Equal(t, http.StatusOK, tracesRR.Code)
	require.Contains(t, tracesRR.Body.String(), "req-1")
	require.Contains(t, tracesRR.Body.String(), "trace-1")
	require.Contains(
		t,
		tracesRR.Body.String(),
		"http://127.0.0.1:3000/project/local-dev/traces/trace-1",
	)
	require.NotContains(t, tracesRR.Body.String(), "req-2")

	metaURL := snap.Debug.RecentTraces[0].MetaURL
	metaRR := httptest.NewRecorder()
	metaReq := httptest.NewRequest(http.MethodGet, metaURL, nil)
	handler.ServeHTTP(metaRR, metaReq)
	require.Equal(t, http.StatusOK, metaRR.Code)
	require.Contains(t, metaRR.Body.String(), "req-1")
}

func TestServiceDebugEndpoints_ServesCompressedEvents(t *testing.T) {
	t.Parallel()

	debugRoot := t.TempDir()
	now := time.Date(2026, 3, 6, 18, 10, 0, 0, time.UTC)
	traceDir := writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:1",
		"req-1",
		now,
		"trace-1",
	)
	gzipDebugTraceEventsFixture(t, traceDir)

	svc := New(Config{DebugDir: debugRoot})
	handler := svc.Handler()
	snap := svc.Snapshot()
	require.Len(t, snap.Debug.RecentTraces, 1)
	require.NotEmpty(t, snap.Debug.RecentTraces[0].EventsURL)

	eventsRR := httptest.NewRecorder()
	eventsReq := httptest.NewRequest(
		http.MethodGet,
		snap.Debug.RecentTraces[0].EventsURL,
		nil,
	)
	handler.ServeHTTP(eventsRR, eventsReq)
	require.Equal(t, http.StatusOK, eventsRR.Code)
	require.Equal(
		t,
		debugEventsMIMEType,
		eventsRR.Header().Get(headerContentType),
	)
	require.Contains(t, eventsRR.Body.String(), "trace.start")
}

func TestServiceHandleDebugFile_CompressedEventsErrors(t *testing.T) {
	t.Parallel()

	debugRoot := t.TempDir()
	svc := New(Config{DebugDir: debugRoot})
	handler := svc.Handler()

	missingRR := httptest.NewRecorder()
	missingReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugFile+"?"+url.Values{
			queryTrace: []string{"20260306/missing"},
			queryName:  []string{debugEventsFileName},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(missingRR, missingReq)
	require.Equal(t, http.StatusBadRequest, missingRR.Code)
	require.Contains(t, missingRR.Body.String(), "debug trace not found")

	tracePath := filepath.Join(debugRoot, "20260306", "trace")
	require.NoError(t, os.MkdirAll(tracePath, 0o755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(tracePath, debugEventsFileName+".gz"),
			[]byte("bad"),
			0o600,
		),
	)

	badGzipRR := httptest.NewRecorder()
	badGzipReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugFile+"?"+url.Values{
			queryTrace: []string{"20260306/trace"},
			queryName:  []string{debugEventsFileName},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(badGzipRR, badGzipReq)
	require.Equal(t, http.StatusBadRequest, badGzipRR.Code)
	require.Contains(t, badGzipRR.Body.String(), "open gzip reader")
}

func TestServiceClearAndValidationPaths(t *testing.T) {
	t.Parallel()

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	for _, name := range []string{"job-a", "job-b"} {
		_, err = cronSvc.Add(&cron.Job{
			Name:    name,
			Enabled: true,
			Schedule: cron.Schedule{
				Kind:  cron.ScheduleKindEvery,
				Every: "1m",
			},
			Message: "collect cpu",
			UserID:  "u1",
		})
		require.NoError(t, err)
	}

	svc := New(Config{Cron: cronSvc})
	handler := svc.Handler()

	methodRR := httptest.NewRecorder()
	methodReq := httptest.NewRequest(http.MethodGet, routeJobRun, nil)
	handler.ServeHTTP(methodRR, methodReq)
	require.Equal(t, http.StatusMethodNotAllowed, methodRR.Code)

	missingRR := httptest.NewRecorder()
	missingReq := httptest.NewRequest(
		http.MethodPost,
		routeJobRemove,
		strings.NewReader(""),
	)
	missingReq.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	handler.ServeHTTP(missingRR, missingReq)
	require.Equal(t, http.StatusSeeOther, missingRR.Code)
	require.Contains(
		t,
		missingRR.Header().Get("Location"),
		"job_id+is+required",
	)

	clearRR := httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPost, routeJobsClear, nil)
	handler.ServeHTTP(clearRR, clearReq)
	require.Equal(t, http.StatusSeeOther, clearRR.Code)
	require.Empty(t, cronSvc.List())
}

func TestServiceWithoutCron(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	handler := svc.Handler()

	statusRR := httptest.NewRecorder()
	statusReq := httptest.NewRequest(http.MethodGet, routeStatusJSON, nil)
	handler.ServeHTTP(statusRR, statusReq)
	require.Equal(t, http.StatusOK, statusRR.Code)
	require.Contains(t, statusRR.Body.String(), `"enabled": false`)

	clearRR := httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPost, routeJobsClear, nil)
	handler.ServeHTTP(clearRR, clearReq)
	require.Equal(t, http.StatusNotFound, clearRR.Code)
}

func TestServiceSnapshotIncludesUploadsAndExec(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	uploadsRoot := filepath.Join(stateDir, defaultUploadsDir)
	relPath := filepath.ToSlash(
		filepath.Join("telegram", "u1", "session-1", "clip.mp4"),
	)
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(filepath.Join(uploadsRoot, relPath)), 0o755),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(uploadsRoot, relPath),
			[]byte("video"),
			0o600,
		),
	)

	svc := New(Config{
		StateDir: stateDir,
		Exec:     octool.NewManager(),
	})
	snap := svc.Snapshot()
	require.True(t, snap.Exec.Enabled)
	require.Equal(t, 0, snap.Exec.SessionCount)
	require.True(t, snap.Uploads.Enabled)
	require.Equal(t, 1, snap.Uploads.FileCount)
	require.Equal(t, "clip.mp4", snap.Uploads.Files[0].Name)
	require.Equal(t, "telegram", snap.Uploads.Files[0].Channel)
	require.Equal(t, "u1", snap.Uploads.Files[0].UserID)
	require.Equal(t, "session-1", snap.Uploads.Files[0].SessionID)
	require.Len(t, snap.Uploads.Sessions, 1)
	require.Len(t, snap.Uploads.KindCounts, 1)
	require.Equal(t, "video", snap.Uploads.KindCounts[0].Kind)
	require.Equal(t, 1, snap.Uploads.KindCounts[0].Count)

	handler := svc.Handler()

	execRR := httptest.NewRecorder()
	execReq := httptest.NewRequest(
		http.MethodGet,
		routeExecSessionsJSON,
		nil,
	)
	handler.ServeHTTP(execRR, execReq)
	require.Equal(t, http.StatusOK, execRR.Code)
	require.Contains(t, execRR.Body.String(), "[]")

	uploadsRR := httptest.NewRecorder()
	uploadsReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadsJSON,
		nil,
	)
	handler.ServeHTTP(uploadsRR, uploadsReq)
	require.Equal(t, http.StatusOK, uploadsRR.Code)
	require.Contains(t, uploadsRR.Body.String(), "clip.mp4")
	require.Contains(t, uploadsRR.Body.String(), `"kind": "video"`)

	openRR := httptest.NewRecorder()
	openReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile+"?"+url.Values{
			queryPath: []string{relPath},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(openRR, openReq)
	require.Equal(t, http.StatusOK, openRR.Code)
	require.Equal(t, "video", openRR.Body.String())
	require.Equal(t, "5", openRR.Header().Get(headerContentLength))

	downloadRR := httptest.NewRecorder()
	downloadReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile+"?"+url.Values{
			queryPath:     []string{relPath},
			queryDownload: []string{"1"},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(downloadRR, downloadReq)
	require.Equal(t, http.StatusOK, downloadRR.Code)
	require.Contains(
		t,
		downloadRR.Header().Get("Content-Disposition"),
		"clip.mp4",
	)
	require.Equal(
		t,
		"5",
		downloadRR.Header().Get(headerContentLength),
	)
}

func TestServiceUploadJSONFilters(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	_, err = store.Save(
		context.Background(),
		uploads.Scope{
			Channel:   "telegram",
			UserID:    "u1",
			SessionID: "session-1",
		},
		"clip.mp4",
		[]byte("video"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithMetadata(
		context.Background(),
		uploads.Scope{
			Channel:   "telegram",
			UserID:    "u2",
			SessionID: "session-2",
		},
		"report.pdf",
		"application/pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithInfo(
		context.Background(),
		uploads.Scope{
			Channel:   "telegram",
			UserID:    "u2",
			SessionID: "session-2",
		},
		"derived-frame.png",
		uploads.FileMetadata{
			MimeType: "image/png",
			Source:   uploads.SourceDerived,
		},
		[]byte("png"),
	)
	require.NoError(t, err)

	svc := New(Config{StateDir: stateDir})
	handler := svc.Handler()

	filesRR := httptest.NewRecorder()
	filesReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadsJSON+"?"+url.Values{
			querySessionID: []string{"session-2"},
			queryKind:      []string{"pdf"},
			queryMimeType:  []string{"application/pdf"},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(filesRR, filesReq)
	require.Equal(t, http.StatusOK, filesRR.Code)
	require.Contains(t, filesRR.Body.String(), "report.pdf")
	require.NotContains(t, filesRR.Body.String(), "clip.mp4")

	sourceRR := httptest.NewRecorder()
	sourceReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadsJSON+"?"+url.Values{
			queryUserID: []string{"u2"},
			querySource: []string{uploads.SourceDerived},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(sourceRR, sourceReq)
	require.Equal(t, http.StatusOK, sourceRR.Code)
	require.Contains(t, sourceRR.Body.String(), "derived-frame.png")
	require.NotContains(t, sourceRR.Body.String(), "report.pdf")

	sessionsRR := httptest.NewRecorder()
	sessionsReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadSessions+"?"+url.Values{
			queryUserID: []string{"u2"},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(sessionsRR, sessionsReq)
	require.Equal(t, http.StatusOK, sessionsRR.Code)
	require.Contains(t, sessionsRR.Body.String(), "session-2")
	require.NotContains(t, sessionsRR.Body.String(), "session-1")
}

func TestAdminRuntimeHelpers(t *testing.T) {
	t.Parallel()

	exitCode := 7
	view := execSessionViewFromSession(octool.ProcessSession{
		SessionID: " sess-1 ",
		Command:   " echo hi ",
		Status:    " running ",
		StartedAt: " start ",
		DoneAt:    " done ",
		ExitCode:  &exitCode,
	})
	require.Equal(t, "sess-1", view.SessionID)
	require.Equal(t, "echo hi", view.Command)
	require.Equal(t, "running", view.Status)
	require.Equal(t, "start", view.StartedAt)
	require.Equal(t, "done", view.DoneAt)
	require.NotNil(t, view.ExitCode)
	require.Equal(t, exitCode, *view.ExitCode)

	require.Equal(t, "image", uploadKindFromName("frame.PNG"))
	require.Equal(t, "audio", uploadKindFromName("voice.ogg"))
	require.Equal(t, "video", uploadKindFromName("clip.MOV"))
	require.Equal(t, "pdf", uploadKindFromName("doc.pdf"))
	require.Equal(t, "file", uploadKindFromName("notes.txt"))
	require.Equal(
		t,
		"video",
		uploadKindFromFile(uploads.ListedFile{
			Name:     "video-note",
			MimeType: "video/mp4",
		}),
	)
}

func TestServiceUploadAndDebugValidationErrors(t *testing.T) {
	t.Parallel()

	svc := New(Config{StateDir: t.TempDir()})
	handler := svc.Handler()

	uploadRR := httptest.NewRecorder()
	uploadReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile,
		nil,
	)
	handler.ServeHTTP(uploadRR, uploadReq)
	require.Equal(t, http.StatusBadRequest, uploadRR.Code)

	debugRR := httptest.NewRecorder()
	debugReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugFile,
		nil,
	)
	handler.ServeHTTP(debugRR, debugReq)
	require.Equal(t, http.StatusBadRequest, debugRR.Code)
}

func TestResolveUploadFile_InvalidPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "telegram", "u1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	filePath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))

	_, err := resolveUploadFile(root, "../clip.mp4")
	require.Error(t, err)

	_, err = resolveUploadFile(root, "telegram/u1")
	require.Error(t, err)

	_, err = resolveUploadFile(
		root,
		"telegram/u1/clip.mp4"+uploads.MetadataSuffix,
	)
	require.Error(t, err)

	got, err := resolveUploadFile(root, "telegram/u1/clip.mp4")
	require.NoError(t, err)
	require.Equal(t, filePath, got)
}

func TestServiceUploadEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	svc := New(Config{StateDir: t.TempDir()})
	handler := svc.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, routeUploadFile, nil)
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestDebugHelpers(t *testing.T) {
	t.Parallel()

	svc := New(Config{DebugDir: t.TempDir()})
	require.Empty(t, svc.debugFileURL("", debugMetaFileName))
	require.Empty(t, svc.debugFileURL("x/y", "notes.txt"))

	items := []debugTraceView{
		{SessionID: "s1"},
		{SessionID: "s2"},
	}
	limited := limitDebugTraces(items, 1)
	require.Len(t, limited, 1)
	require.Equal(t, "s1", limited[0].SessionID)

	require.False(t, fileExists(filepath.Join(t.TempDir(), "missing")))
}

func TestReadDebugTrace_RejectsEscapeAndBadJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bySession := filepath.Join(root, debugBySessionDir, "session-1")
	traceDir := filepath.Join(root, "20260307", "trace")
	require.NoError(t, os.MkdirAll(bySession, 0o755))
	require.NoError(t, os.MkdirAll(traceDir, 0o755))

	refPath := filepath.Join(bySession, debugMetaTraceRefName)
	svc := New(Config{DebugDir: root})

	require.NoError(t, os.WriteFile(refPath, []byte("{"), 0o600))
	_, ok, err := svc.readDebugTrace(
		root,
		filepath.Join(root, debugBySessionDir),
		refPath,
		"",
	)
	require.Error(t, err)
	require.False(t, ok)

	ref := `{
  "trace_dir":"../../../../escape",
  "started_at":"2026-03-07T00:00:00Z"
}`
	require.NoError(t, os.WriteFile(refPath, []byte(ref), 0o600))
	_, ok, err = svc.readDebugTrace(
		root,
		filepath.Join(root, debugBySessionDir),
		refPath,
		"",
	)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestHandleIndex_RendersUploadPreviews(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.Save(context.Background(), scope, "frame.png", []byte("png"))
	require.NoError(t, err)
	_, err = store.Save(context.Background(), scope, "note.mp3", []byte("mp3"))
	require.NoError(t, err)
	_, err = store.Save(context.Background(), scope, "clip.mp4", []byte("mp4"))
	require.NoError(t, err)
	_, err = store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithMetadata(
		context.Background(),
		scope,
		"video-note",
		"video/mp4",
		[]byte("mp4"),
	)
	require.NoError(t, err)

	svc := New(Config{
		StateDir:   stateDir,
		GatewayURL: "http://127.0.0.1:8080",
		AdminURL:   "http://127.0.0.1:19789",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeSessions, nil)

	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), "<img src=\"uploads/file?")
	require.Contains(t, rr.Body.String(), "<audio controls")
	require.Contains(t, rr.Body.String(), "<video controls")
	require.Contains(t, rr.Body.String(), ">open preview</a>")
	require.Contains(t, rr.Body.String(), "<code>video/mp4</code>")
}

func TestRelativeRequestReference(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"overview",
		relativePathReference(routeIndex, routeOverview),
	)
	require.Equal(
		t,
		"../../skills?notice=ok#skills-admin",
		relativeRequestReference(
			routeSkillsRefresh,
			"/skills?notice=ok#skills-admin",
		),
	)
}

func TestRelativeRequestReference_EdgeCases(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		currentPathSegment,
		relativePathReference(
			routeSkillsPage+rootPath,
			routeSkillsPage,
		),
	)

	type testCase struct {
		name        string
		requestPath string
		target      string
		want        string
	}

	cases := []testCase{
		{
			name:        "nested page",
			requestPath: "/skills/setup/",
			target:      routeSkillsPage,
			want:        parentPathSegment,
		},
		{
			name:        "plain relative target",
			requestPath: routeOverview,
			target:      "skills",
			want:        "skills",
		},
		{
			name:        "absolute url",
			requestPath: routeOverview,
			target:      "https://example.com/skills",
			want:        "https://example.com/skills",
		},
		{
			name:        "scheme relative",
			requestPath: routeOverview,
			target:      "//example.com/skills",
			want:        "//example.com/skills",
		},
		{
			name:        "invalid escape",
			requestPath: routeOverview,
			target:      "/%zz",
			want:        "/%zz",
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(
				t,
				tt.want,
				relativeRequestReference(
					tt.requestPath,
					tt.target,
				),
			)
		})
	}
}

func TestIsHTMLContentType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		contentType string
		want        bool
	}

	cases := []testCase{
		{
			name:        "html with charset",
			contentType: "text/html; charset=utf-8",
			want:        true,
		},
		{
			name:        "html with invalid params",
			contentType: " Text/HTML; charset",
			want:        true,
		},
		{
			name:        "json",
			contentType: "application/json",
			want:        false,
		},
		{
			name:        "empty",
			contentType: "",
			want:        false,
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(
				t,
				tt.want,
				isHTMLContentType(tt.contentType),
			)
		})
	}
}

func TestRewriteHTMLBody(t *testing.T) {
	t.Parallel()

	body := []byte(`
<html><body>
<a href="/skills" data-path="/skills">Skills</a>
<div
  data-page-state-path="/api/page/state?view=overview"
  data-chat-history-path="/api/chats/history"></div>
<form action="/api/skills/refresh">
<button formaction="/api/cron/jobs/run">Run</button>
</form>
<img src="/uploads/file?path=clip.mp4">
</body></html>`)

	got, ok := rewriteHTMLBody(
		routeOverview,
		"text/html; charset=utf-8",
		body,
	)
	require.True(t, ok)
	require.Contains(t, string(got), `href="skills"`)
	require.Contains(
		t,
		string(got),
		`action="api/skills/refresh"`,
	)
	require.Contains(
		t,
		string(got),
		`formaction="api/cron/jobs/run"`,
	)
	require.Contains(
		t,
		string(got),
		`src="uploads/file?path=clip.mp4"`,
	)
	require.Contains(t, string(got), `data-path="/skills"`)
	require.Contains(
		t,
		string(got),
		`data-page-state-path="api/page/state?view=overview"`,
	)
	require.Contains(
		t,
		string(got),
		`data-chat-history-path="api/chats/history"`,
	)
}

func TestRewriteHTMLBodySkipsNonHTML(t *testing.T) {
	t.Parallel()

	jsonBody := []byte(`{"href":"/skills"}`)
	got, ok := rewriteHTMLBody(
		routeOverview,
		"application/json",
		jsonBody,
	)
	require.False(t, ok)
	require.Equal(t, jsonBody, got)

	got, ok = rewriteHTMLBody(routeOverview, htmlMediaType, nil)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestRelativePathInternalHelpers(t *testing.T) {
	t.Parallel()

	require.Nil(t, wrapRelativeLinks(nil))
	require.Nil(t, wrapRelativeLinksFunc(nil))

	writer := newBufferedResponseWriter()
	require.NotNil(t, writer.Header())

	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusAccepted)
	writer.Flush()

	require.Equal(t, http.StatusCreated, writer.status)
	require.True(t, writer.wroteHeader)

	require.Equal(t, rootPath, requestBaseDir(""))
	require.Equal(t, "/skills", requestBaseDir("skills/setup"))
	require.Equal(t, rootPath, requestPath(nil))
	require.Equal(t, rootPath, requestPath(&http.Request{}))
	require.Equal(
		t,
		routeSkillsPage,
		requestPath(&http.Request{
			URL: &url.URL{Path: routeSkillsPage},
		}),
	)

	rewriteHTMLReferences(nil, routeOverview)
}

func TestServiceUploadsJSON_RewritesGeneratedNames(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.SaveWithMetadata(
		context.Background(),
		scope,
		"file_10.mp4",
		"video/mp4",
		[]byte("mp4"),
	)
	require.NoError(t, err)

	svc := New(Config{StateDir: stateDir})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeUploadsJSON, nil)

	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), "\"name\": \"video.mp4\"")
	require.NotContains(t, rr.Body.String(), "\"name\": \"file_10.mp4\"")
}

func TestDebugStatusForSession_SkipsBadTraceRefs(t *testing.T) {
	t.Parallel()

	debugRoot := t.TempDir()
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:1",
		"req-1",
		now,
		"trace-1",
	)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:2",
		"req-2",
		now.Add(-time.Minute),
		"trace-2",
	)

	badRefDir := filepath.Join(
		debugRoot,
		debugBySessionDir,
		"telegram:dm:1",
		now.Format("20060102"),
		"bad-ref",
	)
	require.NoError(t, os.MkdirAll(badRefDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(badRefDir, debugMetaTraceRefName),
		[]byte("{"),
		0o600,
	))

	status := New(Config{DebugDir: debugRoot}).debugStatusForSession(
		"telegram:dm:1",
	)
	require.True(t, status.Enabled)
	require.Equal(t, 1, status.SessionCount)
	require.Equal(t, 1, status.TraceCount)
	require.Len(t, status.Sessions, 1)
	require.Equal(t, "telegram:dm:1", status.Sessions[0].SessionID)
	require.NotEmpty(t, status.Error)
}

func TestUploadRuntimeHelpers_LimitsAndFilters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	listed := []uploads.ListedFile{
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u2",
				SessionID: "s2",
			},
			Name:         "file_10.mp4",
			RelativePath: "telegram/u2/s2/file_10.mp4",
			MimeType:     "video/mp4",
			SizeBytes:    7,
			ModifiedAt:   now,
		},
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u1",
				SessionID: "s1",
			},
			Name:         "report.pdf",
			RelativePath: "telegram/u1/s1/report.pdf",
			MimeType:     "application/pdf",
			Source:       uploads.SourceDerived,
			SizeBytes:    5,
			ModifiedAt:   now.Add(-time.Minute),
		},
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u1",
				SessionID: "s1",
			},
			Name:         "voice.ogg",
			RelativePath: "telegram/u1/s1/voice.ogg",
			MimeType:     "audio/ogg",
			Source:       uploads.SourceInbound,
			SizeBytes:    3,
			ModifiedAt:   now.Add(-2 * time.Minute),
		},
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u0",
				SessionID: "s3",
			},
			Name:         "clip.mp4",
			RelativePath: "telegram/u0/s3/clip.mp4",
			MimeType:     "video/mp4",
			Source:       uploads.SourceDerived,
			SizeBytes:    9,
			ModifiedAt:   now,
		},
	}

	views, totalBytes := uploadViewsFromList(listed, 1)
	require.Equal(t, int64(24), totalBytes)
	require.Len(t, views, 1)
	require.Equal(t, "video.mp4", views[0].Name)
	require.Contains(t, views[0].OpenURL, queryPath+"=")
	require.Contains(t, views[0].DownloadURL, queryDownload+"=1")

	sessions := uploadSessionsFromList(listed, 0)
	require.Len(t, sessions, 3)
	require.Equal(t, "u0", sessions[0].UserID)
	require.Equal(t, "u2", sessions[1].UserID)
	require.Equal(t, "u1", sessions[2].UserID)

	limitedSessions := uploadSessionsFromList(listed, 2)
	require.Len(t, limitedSessions, 2)

	kindCounts := uploadKindCountsFromList(listed)
	require.Len(t, kindCounts, 3)
	require.Equal(t, "video", kindCounts[0].Kind)
	require.Equal(t, 2, kindCounts[0].Count)

	sourceCounts := uploadSourceCountsFromList(listed)
	require.Len(t, sourceCounts, 3)
	require.Equal(t, uploads.SourceDerived, sourceCounts[0].Source)
	require.Equal(t, 2, sourceCounts[0].Count)
	require.Equal(t, "unknown", sourceCounts[2].Source)

	filtered := filterUploadList(
		listed,
		uploadFilters{
			UserID:   "u1",
			Kind:     "pdf",
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
	)
	require.Len(t, filtered, 1)
	require.Equal(t, "report.pdf", filtered[0].Name)

	require.Equal(t, listed, filterUploadList(listed, uploadFilters{}))
	require.Nil(t, filterUploadList(nil, uploadFilters{}))
	require.Nil(t, uploadKindCountsFromList(nil))
	require.Nil(t, uploadSourceCountsFromList(nil))
}

func TestUploadsStatusFiltered_StateErrors(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		uploadsStatus{},
		New(Config{}).uploadsStatusFiltered(uploadFilters{}, 0, 0),
	)

	badState := filepath.Join(t.TempDir(), "state-file")
	require.NoError(t, os.WriteFile(badState, []byte("x"), 0o600))

	status := New(Config{StateDir: badState}).uploadsStatusFiltered(
		uploadFilters{},
		0,
		0,
	)
	require.False(t, status.Enabled)
	require.NotEmpty(t, status.Error)
}

func TestServiceHelperFunctions(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusCreated, map[string]string{"ok": "yes"})
	require.Equal(t, http.StatusCreated, rr.Code)
	require.Contains(t, rr.Body.String(), "\"ok\": \"yes\"")

	errRR := httptest.NewRecorder()
	writeJSON(
		errRR,
		http.StatusOK,
		map[string]any{"bad": make(chan int)},
	)
	require.Equal(t, http.StatusInternalServerError, errRR.Code)

	require.Empty(t, fallbackJobName(nil))
	require.Equal(
		t,
		"job-1",
		fallbackJobName(&cron.Job{ID: " job-1 "}),
	)
	require.Equal(
		t,
		"named",
		fallbackJobName(&cron.Job{Name: " named ", ID: "job-2"}),
	)
	require.Equal(t, "short", summarizeText(" short ", 0))
	require.Equal(t, "abc...", summarizeText("abcdef", 3))
	require.Equal(t, 5, intFromMap(5))
	require.Zero(t, intFromMap("bad"))
	require.Nil(t, stringSliceFromMap(nil))
	require.Equal(
		t,
		[]string{"a", "b"},
		stringSliceFromMap([]string{"b", "a"}),
	)
	require.Nil(t, stringSliceFromMap("bad"))

	now := time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC)
	require.Equal(t, "-", formatTime(time.Time{}))
	require.Equal(t, "-", formatTime((*time.Time)(nil)))
	require.Equal(
		t,
		now.Local().Format(formatTimeLayout),
		formatTime(now),
	)
	require.Equal(t, "-", formatTime("bad"))
	require.Equal(t, "-", formatUptime(time.Time{}, now))
	require.Equal(t, "0s", formatUptime(now, now.Add(-time.Minute)))

	require.Equal(t, uploadFilters{}, uploadFiltersFromRequest(nil))
	req := httptest.NewRequest(
		http.MethodGet,
		"/?"+url.Values{
			queryChannel:   []string{" telegram "},
			queryUserID:    []string{" u1 "},
			querySessionID: []string{" s1 "},
			queryKind:      []string{" pdf "},
			queryMimeType:  []string{" application/pdf "},
			querySource:    []string{" derived "},
		}.Encode(),
		nil,
	)
	require.Equal(
		t,
		uploadFilters{
			Channel:   "telegram",
			UserID:    "u1",
			SessionID: "s1",
			Kind:      "pdf",
			MimeType:  "application/pdf",
			Source:    "derived",
		},
		uploadFiltersFromRequest(req),
	)
}

func TestServiceResolveDebugFileAndMethodChecks(t *testing.T) {
	t.Parallel()

	debugDir := t.TempDir()
	traceDir := filepath.Join(debugDir, "20260307", "trace")
	require.NoError(t, os.MkdirAll(traceDir, 0o755))
	metaPath := filepath.Join(traceDir, debugMetaFileName)
	require.NoError(t, os.WriteFile(metaPath, []byte("{}"), 0o600))

	svc := New(Config{DebugDir: debugDir})
	got, err := svc.resolveDebugFile(
		"20260307/trace",
		debugMetaFileName,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, metaPath, got)

	eventsPath := filepath.Join(traceDir, debugEventsFileName)
	require.NoError(
		t,
		os.WriteFile(eventsPath, []byte("{\"kind\":\"trace.start\"}\n"), 0o600),
	)
	gzipDebugTraceEventsFixture(t, traceDir)

	got, err = svc.resolveDebugFile(
		"20260307/trace",
		debugEventsFileName,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, eventsPath+".gz", got)

	emptyTraceDir := filepath.Join(debugDir, "20260307", "empty-trace")
	require.NoError(t, os.MkdirAll(emptyTraceDir, 0o755))
	_, err = svc.resolveDebugFile(
		"20260307/empty-trace",
		debugEventsFileName,
		"",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "debug file not found")

	serviceLog := filepath.Join(debugDir, "services", "browser-server.log")
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(serviceLog), 0o755),
	)
	require.NoError(
		t,
		os.WriteFile(serviceLog, []byte("ready\n"), 0o600),
	)

	got, err = svc.resolveDebugFile("", "", "services/browser-server.log")
	require.NoError(t, err)
	require.Equal(t, serviceLog, got)

	_, err = svc.resolveDebugFile("", debugMetaFileName, "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("20260307/trace", "notes.txt", "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("../escape", debugMetaFileName, "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("20260307/missing", debugMetaFileName, "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("", "", "../escape")
	require.Error(t, err)

	_, err = svc.resolveDebugTraceDir("")
	require.Error(t, err)
	_, err = New(Config{}).resolveDebugTraceDir("20260307/trace")
	require.Error(t, err)

	fileTracePath := filepath.Join(debugDir, "20260307", "trace-file")
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(fileTracePath), 0o755),
	)
	require.NoError(
		t,
		os.WriteFile(fileTracePath, []byte("x"), 0o600),
	)
	_, err = svc.resolveDebugTraceDir("20260307/trace-file")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")

	handler := svc.Handler()
	routes := []string{
		routeStatusJSON,
		routeJobsJSON,
		routeExecSessionsJSON,
		routeUploadsJSON,
		routeUploadSessions,
		routeDebugSessionsJSON,
		routeDebugTracesJSON,
	}
	for _, route := range routes {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, route, nil)
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	}
}
