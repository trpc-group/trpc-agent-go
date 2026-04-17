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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubRuntimeConfigProvider struct {
	status RuntimeConfigStatus
	err    error

	saveErr    error
	resetErr   error
	saveKey    string
	saveValue  string
	saveCount  int
	resetKey   string
	resetCount int
}

func (p *stubRuntimeConfigProvider) RuntimeConfigStatus() (
	RuntimeConfigStatus,
	error,
) {
	if p == nil {
		return RuntimeConfigStatus{}, nil
	}
	return p.status, p.err
}

func (p *stubRuntimeConfigProvider) SaveRuntimeConfigValue(
	key string,
	value string,
) error {
	if p == nil {
		return nil
	}
	p.saveCount++
	p.saveKey = key
	p.saveValue = value
	return p.saveErr
}

func (p *stubRuntimeConfigProvider) ResetRuntimeConfigValue(
	key string,
) error {
	if p == nil {
		return nil
	}
	p.resetCount++
	p.resetKey = key
	return p.resetErr
}

func TestServiceHandlerRendersConfigPage(t *testing.T) {
	t.Parallel()

	const malformedSummaryClose = `</div>
                  </div>
              </summary>`

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			status: RuntimeConfigStatus{
				ConfigPath: "/tmp/openclaw.yaml",
				Sections: []RuntimeConfigSection{{
					Key:     "skills",
					Title:   "Skills",
					Summary: "Skill loading and fallback policy.",
					Fields: []RuntimeConfigField{{
						Key:                   "skills.max_loaded_skills",
						Title:                 "Max Loaded Skills",
						Summary:               "Cap the active skill set.",
						InputType:             configInputNumber,
						ApplyMode:             configApplyRestart,
						EditorValue:           "10",
						ConfiguredValue:       "10",
						ConfiguredSource:      configSourceExplicit,
						ConfiguredSourceLabel: "Explicit in config",
						RuntimeValue:          "6",
						RuntimeSourceLabel:    "Current runtime",
						PendingRestart:        true,
						Resettable:            true,
					}},
				}},
			},
		}),
	)

	req := httptest.NewRequest(http.MethodGet, routeConfigPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Runtime Config")
	require.Contains(t, body, "Max Loaded Skills")
	require.Contains(t, body, "/tmp/openclaw.yaml")
	require.Contains(t, body, `<details class="config-field"`)
	require.Contains(t, body, `<summary class="config-field-summary">`)
	require.NotContains(t, body, malformedSummaryClose)
	require.Contains(t, body, `action="api/config/save"`)
	require.Contains(t, body, `formaction="api/config/reset"`)
	require.Contains(t, body, "Pending restart")
}

func TestServiceHandlerRendersReadOnlyConfigField(t *testing.T) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			status: RuntimeConfigStatus{
				ConfigPath: "/tmp/openclaw.yaml",
				Sections: []RuntimeConfigSection{{
					Key:   "runtime",
					Title: "Runtime",
					Fields: []RuntimeConfigField{{
						Key:                "runtime.effective_path",
						Title:              "Effective PATH",
						Summary:            "Read-only path diagnostics.",
						InputType:          configInputReadOnly,
						ApplyMode:          configApplyHot,
						EditorValue:        "/usr/local/bin:/usr/bin",
						RuntimeValue:       "/usr/local/bin:/usr/bin",
						RuntimeSourceLabel: "Current process PATH.",
					}},
				}},
			},
		}),
	)

	req := httptest.NewRequest(http.MethodGet, routeConfigPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Read-only runtime diagnostics.")
	require.NotContains(t, body, `class="config-form"`)
	require.Contains(t, body, "Effective PATH")
}

func TestServiceHandlerRendersConfigPageRestartCTA(t *testing.T) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			status: RuntimeConfigStatus{
				ConfigPath: "/tmp/openclaw.yaml",
				Sections: []RuntimeConfigSection{{
					Key:   "skills",
					Title: "Skills",
					Fields: []RuntimeConfigField{{
						Key:            "skills.max_loaded_skills",
						Title:          "Max Loaded Skills",
						PendingRestart: true,
					}},
				}},
			},
		}),
		WithRuntimeLifecycleProvider(
			&stubRuntimeLifecycleProvider{},
		),
	)

	req := httptest.NewRequest(http.MethodGet, routeConfigPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Open Runtime Control")
	require.Contains(t, body, `href="runtime-control"`)
	require.Contains(t, body, "1 saved field")
	require.Contains(
		t,
		body,
		"still need a restart before the running runtime will use them.",
	)
}

func TestServiceHandlerRendersConfigPageRuntimeControlLink(
	t *testing.T,
) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			status: RuntimeConfigStatus{
				ConfigPath: "/tmp/openclaw.yaml",
				Sections: []RuntimeConfigSection{{
					Key:   "skills",
					Title: "Skills",
				}},
			},
		}),
		WithRuntimeLifecycleProvider(
			&stubRuntimeLifecycleProvider{},
		),
	)

	req := httptest.NewRequest(http.MethodGet, routeConfigPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Runtime control")
	require.Contains(t, body, "Restart and version actions")
	require.Contains(t, body, "Open Runtime Control")
}

func TestHandleSaveRuntimeConfig(t *testing.T) {
	t.Parallel()

	provider := &stubRuntimeConfigProvider{}
	svc := New(Config{}, WithRuntimeConfigProvider(provider))

	values := url.Values{
		formConfigFieldKey: {"skills.max_loaded_skills"},
		formConfigValue:    {"10"},
		formReturnPath:     {routeConfigPage},
		formReturnTo:       {"config-field-skills.max_loaded_skills"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, "skills.max_loaded_skills", provider.saveKey)
	require.Equal(t, "10", provider.saveValue)
	location := rr.Header().Get("Location")
	require.Contains(t, location, "config?notice=Saved+config+field.")
	require.Contains(
		t,
		location,
		"Restart+the+runtime+to+apply+restart-bound+changes.",
	)
	require.True(
		t,
		strings.HasSuffix(
			location,
			"#config-field-skills.max_loaded_skills",
		),
	)
}

func TestHandleResetRuntimeConfig(t *testing.T) {
	t.Parallel()

	provider := &stubRuntimeConfigProvider{}
	svc := New(Config{}, WithRuntimeConfigProvider(provider))

	values := url.Values{
		formConfigFieldKey: {"skills.max_loaded_skills"},
		formReturnPath:     {routeConfigPage},
		formReturnTo:       {"config-field-skills.max_loaded_skills"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigReset,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, "skills.max_loaded_skills", provider.resetKey)
	location := rr.Header().Get("Location")
	require.Contains(
		t,
		location,
		"config?notice=Reset+config+field+to+inherited+behavior.",
	)
	require.True(
		t,
		strings.HasSuffix(
			location,
			"#config-field-skills.max_loaded_skills",
		),
	)
}

func TestServiceRuntimeConfigStatus_Error(t *testing.T) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			err: errors.New(" status failed "),
		}),
	)

	status := svc.runtimeConfigStatus()
	require.True(t, status.Enabled)
	require.Equal(t, "status failed", status.Error)
}

func TestServiceRuntimeConfigProviderHelpers_NilService(t *testing.T) {
	t.Parallel()

	var svc *Service
	require.Nil(t, svc.runtimeConfigProvider())
	require.False(t, svc.hasRuntimeConfigProvider())
}

func TestHandleConfigJSON(t *testing.T) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			status: RuntimeConfigStatus{
				ConfigPath: "/tmp/openclaw.yaml",
			},
		}),
	)

	req := httptest.NewRequest(http.MethodGet, routeConfigJSON, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var status RuntimeConfigStatus
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &status))
	require.True(t, status.Enabled)
	require.Equal(t, "/tmp/openclaw.yaml", status.ConfigPath)
}

func TestHandleConfigJSON_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	req := httptest.NewRequest(http.MethodPost, routeConfigJSON, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestHandleSaveRuntimeConfig_ProviderError(t *testing.T) {
	t.Parallel()

	provider := &stubRuntimeConfigProvider{
		saveErr: errors.New("write failed"),
	}
	svc := New(Config{}, WithRuntimeConfigProvider(provider))

	values := url.Values{
		formConfigFieldKey: {"skills.max_loaded_skills"},
		formConfigValue:    {"10"},
		formReturnPath:     {routeConfigPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"config?error=write+failed",
	)
}

func TestHandleSaveRuntimeConfig_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	req := httptest.NewRequest(http.MethodGet, routeConfigSave, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestHandleSaveRuntimeConfig_NoProvider(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	values := url.Values{
		formConfigFieldKey: {"skills.max_loaded_skills"},
		formConfigValue:    {"10"},
		formReturnPath:     {routeConfigPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"config?error=runtime+config+provider+is+not+available",
	)
}

func TestHandleSaveRuntimeConfig_MissingField(t *testing.T) {
	t.Parallel()

	svc := New(Config{}, WithRuntimeConfigProvider(&stubRuntimeConfigProvider{}))

	values := url.Values{
		formConfigValue: {"10"},
		formReturnPath:  {routeConfigPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigSave,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"config?error=config+field+is+required",
	)
}

func TestHandleResetRuntimeConfig_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	req := httptest.NewRequest(http.MethodGet, routeConfigReset, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestHandleResetRuntimeConfig_NoProvider(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	values := url.Values{
		formConfigFieldKey: {"skills.max_loaded_skills"},
		formReturnPath:     {routeConfigPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigReset,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"config?error=runtime+config+provider+is+not+available",
	)
}

func TestHandleResetRuntimeConfig_MissingField(t *testing.T) {
	t.Parallel()

	svc := New(Config{}, WithRuntimeConfigProvider(&stubRuntimeConfigProvider{}))

	values := url.Values{
		formReturnPath: {routeConfigPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigReset,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"config?error=config+field+is+required",
	)
}

func TestHandleResetRuntimeConfig_ProviderError(t *testing.T) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeConfigProvider(&stubRuntimeConfigProvider{
			resetErr: errors.New("reset failed"),
		}),
	)

	values := url.Values{
		formConfigFieldKey: {"skills.max_loaded_skills"},
		formReturnPath:     {routeConfigPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeConfigReset,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"config?error=reset+failed",
	)
}
