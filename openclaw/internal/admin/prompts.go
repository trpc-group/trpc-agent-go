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
	"net/http"
	"strings"
)

const (
	routePromptsJSON       = "/api/prompts"
	routePromptRuntimeSave = "/api/prompts/runtime"
	routePromptFileSave    = "/api/prompts/file"
	routePromptFileCreate  = "/api/prompts/file/create"
	routePromptFileDelete  = "/api/prompts/file/delete"

	formPromptBundleKey = "bundle_key"
	formPromptPath      = "path"
	formPromptFileName  = "file_name"
	formPromptContent   = "content"
)

type PromptsProvider interface {
	PromptsStatus() (PromptsStatus, error)
	SavePromptRuntime(bundleKey string, content string) error
	SavePromptFile(bundleKey string, path string, content string) error
	CreatePromptFile(bundleKey string, fileName string, content string) error
	DeletePromptFile(bundleKey string, path string) error
}

type PromptsStatus struct {
	Enabled bool                `json:"enabled"`
	Error   string              `json:"error,omitempty"`
	Bundles []PromptBundleState `json:"bundles,omitempty"`
}

type PromptBundleState struct {
	Key                string            `json:"key,omitempty"`
	Title              string            `json:"title,omitempty"`
	ConfiguredValue    string            `json:"configured_value,omitempty"`
	EffectiveValue     string            `json:"effective_value,omitempty"`
	RuntimeValue       string            `json:"runtime_value,omitempty"`
	RuntimeEditable    bool              `json:"runtime_editable"`
	RuntimeOverride    bool              `json:"runtime_override"`
	CreateEnabled      bool              `json:"create_enabled"`
	CreateDir          string            `json:"create_dir,omitempty"`
	LoadError          string            `json:"load_error,omitempty"`
	Files              []PromptFileState `json:"files,omitempty"`
	SupportsFileEdits  bool              `json:"supports_file_edits"`
	SupportsFileCreate bool              `json:"supports_file_create"`
	SupportsFileDelete bool              `json:"supports_file_delete"`
}

type PromptFileState struct {
	Path      string `json:"path,omitempty"`
	Label     string `json:"label,omitempty"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
	Deletable bool   `json:"deletable"`
}

func (s *Service) promptsStatus() PromptsStatus {
	if s == nil || s.cfg.Prompts == nil {
		return PromptsStatus{}
	}
	status, err := s.cfg.Prompts.PromptsStatus()
	if err != nil {
		return PromptsStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}
	status.Enabled = true
	return status
}

func (s *Service) handlePromptsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.promptsStatus())
}

func (s *Service) handleSavePromptRuntime(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bundleKey, content, returnTo, ok := s.requirePromptPOST(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Prompts.SavePromptRuntime(bundleKey, content); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	message := "Saved runtime prompt override."
	if strings.TrimSpace(content) == "" {
		message = "Cleared runtime prompt override."
	}
	s.redirectWithMessageAt(w, r, queryNotice, message, returnTo)
}

func (s *Service) handleSavePromptFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bundleKey, content, returnTo, ok := s.requirePromptPOST(w, r)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.FormValue(formPromptPath))
	if path == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"path is required",
			returnTo,
		)
		return
	}
	if err := s.cfg.Prompts.SavePromptFile(
		bundleKey,
		path,
		content,
	); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Saved prompt file.",
		returnTo,
	)
}

func (s *Service) handleCreatePromptFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bundleKey, content, returnTo, ok := s.requirePromptPOST(w, r)
	if !ok {
		return
	}
	fileName := strings.TrimSpace(r.FormValue(formPromptFileName))
	if fileName == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"file_name is required",
			returnTo,
		)
		return
	}
	if err := s.cfg.Prompts.CreatePromptFile(
		bundleKey,
		fileName,
		content,
	); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Created prompt file.",
		returnTo,
	)
}

func (s *Service) handleDeletePromptFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bundleKey, _, returnTo, ok := s.requirePromptPOST(w, r)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.FormValue(formPromptPath))
	if path == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"path is required",
			returnTo,
		)
		return
	}
	if err := s.cfg.Prompts.DeletePromptFile(bundleKey, path); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Deleted prompt file.",
		returnTo,
	)
}

func (s *Service) requirePromptPOST(
	w http.ResponseWriter,
	r *http.Request,
) (string, string, string, bool) {
	if s == nil || s.cfg.Prompts == nil {
		http.Error(w, "prompts are not enabled", http.StatusNotFound)
		return "", "", "", false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", "", false
	}
	bundleKey := strings.TrimSpace(r.FormValue(formPromptBundleKey))
	if bundleKey == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"bundle_key is required",
			"",
		)
		return "", "", "", false
	}
	return bundleKey,
		r.FormValue(formPromptContent),
		strings.TrimSpace(r.FormValue(formReturnTo)),
		true
}

const promptsPageTemplateHTML = `
{{define "promptsPage"}}
    <section class="panels">
      <article class="card">
        <h2>Prompt Management</h2>
        <p class="subtle">
          Inspect effective prompts, edit configured files, and apply
          runtime-only overrides without restarting the runtime.
        </p>
        <dl class="meta">
          <dt>Bundles</dt>
          <dd>{{len .Prompts.Bundles}}</dd>
          <dt>JSON</dt>
          <dd><a href="/api/prompts">/api/prompts</a></dd>
          <dt>Persistence</dt>
          <dd>File edits persist. Runtime overrides reset on restart.</dd>
        </dl>
      </article>
    </section>

    <section class="card" style="margin-top: 24px;">
      <h2>Prompt Bundles</h2>
      <p class="subtle">
        File-backed edits flow back into the live runtime immediately. When a
        bundle is backed by a directory, you can add or remove markdown files
        directly from this page.
      </p>
      {{if .Prompts.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Prompts.Error}}
      </div>
      {{end}}
      {{if .Prompts.Bundles}}
      {{range .Prompts.Bundles}}
      {{$bundle := .}}
      <article class="card" style="margin-top: 18px;" id="prompt-{{.Key}}">
        <h3>{{.Title}}</h3>
        <dl class="meta">
          <dt>Configured Files</dt>
          <dd>{{len .Files}}</dd>
          <dt>Runtime Override</dt>
          <dd>{{if .RuntimeOverride}}active{{else}}off{{end}}</dd>
          <dt>Create Dir</dt>
          <dd>{{if .CreateDir}}<code>{{.CreateDir}}</code>{{else}}-{{end}}</dd>
        </dl>
        {{if .LoadError}}
        <div class="notice err" style="margin-top: 12px;">
          {{.LoadError}}
        </div>
        {{end}}

        <div class="panels" style="grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));">
          <article class="card">
            <h4 style="margin: 0 0 10px;">Configured Prompt</h4>
            {{if .ConfiguredValue}}
            <div class="memory-preview">{{.ConfiguredValue}}</div>
            {{else}}
            <p class="empty">No configured prompt content.</p>
            {{end}}
          </article>
          <article class="card">
            <h4 style="margin: 0 0 10px;">Live Runtime Prompt</h4>
            {{if .EffectiveValue}}
            <div class="memory-preview">{{.EffectiveValue}}</div>
            {{else}}
            <p class="empty">Prompt is empty at runtime.</p>
            {{end}}
          </article>
        </div>

        {{if .RuntimeEditable}}
        <div class="card" style="margin-top: 16px;">
          <h4 style="margin: 0 0 10px;">Runtime Override</h4>
          <p class="subtle">
            Leave this empty to fall back to the configured prompt sources.
          </p>
          <form method="post" action="/api/prompts/runtime">
            <input type="hidden" name="bundle_key" value="{{.Key}}">
            <input type="hidden" name="return_path" value="/prompts">
            <input type="hidden" name="return_to" value="prompt-{{.Key}}">
            <textarea
              name="content"
              style="width: 100%; min-height: 180px; margin-top: 12px;"
            >{{.RuntimeValue}}</textarea>
            <div class="actions" style="margin-top: 12px;">
              <button type="submit">Save Runtime Override</button>
            </div>
          </form>
        </div>
        {{end}}

        <div class="card" style="margin-top: 16px;">
          <h4 style="margin: 0 0 10px;">Configured Files</h4>
          {{if .CreateEnabled}}
          <form method="post" action="/api/prompts/file/create">
            <input type="hidden" name="bundle_key" value="{{.Key}}">
            <input type="hidden" name="return_path" value="/prompts">
            <input type="hidden" name="return_to" value="prompt-{{.Key}}">
            <div class="panels" style="grid-template-columns: minmax(0, 240px) minmax(0, 1fr); margin-top: 12px;">
              <div>
                <label for="create-{{.Key}}">New file name</label>
                <input
                  id="create-{{.Key}}"
                  type="text"
                  name="file_name"
                  placeholder="30_local.md"
                  style="width: 100%; margin-top: 8px;"
                >
              </div>
              <div>
                <label for="create-body-{{.Key}}">Initial content</label>
                <textarea
                  id="create-body-{{.Key}}"
                  name="content"
                  style="width: 100%; min-height: 120px; margin-top: 8px;"
                ></textarea>
              </div>
            </div>
            <div class="actions" style="margin-top: 12px;">
              <button class="secondary" type="submit">Create File</button>
            </div>
          </form>
          {{end}}

          {{if .Files}}
          {{range .Files}}
          <div class="card" style="margin-top: 12px;">
            <div style="display: flex; gap: 12px; justify-content: space-between; align-items: baseline; flex-wrap: wrap;">
              <strong>{{.Label}}</strong>
              <code>{{.Path}}</code>
            </div>
            {{if .Error}}
            <div class="notice err" style="margin-top: 12px;">
              {{.Error}}
            </div>
            {{end}}
            <form method="post" action="/api/prompts/file" style="margin-top: 12px;">
              <input type="hidden" name="bundle_key" value="{{$bundle.Key}}">
              <input type="hidden" name="path" value="{{.Path}}">
              <input type="hidden" name="return_path" value="/prompts">
              <input type="hidden" name="return_to" value="prompt-{{$bundle.Key}}">
              <textarea
                name="content"
                style="width: 100%; min-height: 180px;"
              >{{.Content}}</textarea>
              <div class="actions" style="margin-top: 12px;">
                <button type="submit">Save File</button>
              </div>
            </form>
            {{if .Deletable}}
            <form method="post" action="/api/prompts/file/delete" style="margin-top: 8px;">
              <input type="hidden" name="bundle_key" value="{{$bundle.Key}}">
              <input type="hidden" name="path" value="{{.Path}}">
              <input type="hidden" name="return_path" value="/prompts">
              <input type="hidden" name="return_to" value="prompt-{{$bundle.Key}}">
              <div class="actions">
                <button
                  class="warn"
                  type="submit"
                  onclick="return confirm('Delete this prompt file?');"
                >
                  Delete File
                </button>
              </div>
            </form>
            {{end}}
          </div>
          {{end}}
          {{else}}
          <p class="empty">
            {{if .CreateEnabled}}
              No markdown files exist in this bundle yet.
            {{else}}
              No editable prompt files are configured for this bundle.
            {{end}}
          </p>
          {{end}}
        </div>
      </article>
      {{end}}
      {{else}}
      <p class="empty">Prompt management is not available for this runtime.</p>
      {{end}}
    </section>
{{end}}
`
