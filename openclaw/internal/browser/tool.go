//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	actionStatus     = "status"
	actionStart      = "start"
	actionStop       = "stop"
	actionProfiles   = "profiles"
	actionTabs       = "tabs"
	actionOpen       = "open"
	actionFocus      = "focus"
	actionClose      = "close"
	actionSnapshot   = "snapshot"
	actionScreenshot = "screenshot"
	actionNavigate   = "navigate"
	actionConsole    = "console"
	actionCookies    = "cookies"
	actionStorage    = "storage"
	actionPDF        = "pdf"
	actionDownload   = "download"
	actionWaitDL     = "waitDownload"
	actionUpload     = "upload"
	actionDialog     = "dialog"
	actionOffline    = "offline"
	actionHeaders    = "headers"
	actionCreds      = "credentials"
	actionGeo        = "geolocation"
	actionMedia      = "media"
	actionTimezone   = "timezone"
	actionLocale     = "locale"
	actionDevice     = "device"
	actionAct        = "act"
)

const (
	actClick          = "click"
	actType           = "type"
	actPress          = "press"
	actHover          = "hover"
	actScrollIntoView = "scrollIntoView"
	actDrag           = "drag"
	actSelect         = "select"
	actFill           = "fill"
	actResize         = "resize"
	actWait           = "wait"
	actEvaluate       = "evaluate"
	actClose          = "close"
)

const (
	targetHost    = "host"
	targetSandbox = "sandbox"
	targetNode    = "node"
)

const (
	tabActionList   = "list"
	tabActionNew    = "create"
	tabActionSelect = "select"
	tabActionClose  = "close"
)

const (
	stateOpGet   = "get"
	stateOpSet   = "set"
	stateOpClear = "clear"
)

var supportedActions = []string{
	actionStatus,
	actionStart,
	actionStop,
	actionProfiles,
	actionTabs,
	actionOpen,
	actionFocus,
	actionClose,
	actionSnapshot,
	actionScreenshot,
	actionNavigate,
	actionConsole,
	actionCookies,
	actionStorage,
	actionPDF,
	actionDownload,
	actionWaitDL,
	actionUpload,
	actionDialog,
	actionOffline,
	actionHeaders,
	actionCreds,
	actionGeo,
	actionMedia,
	actionTimezone,
	actionLocale,
	actionDevice,
	actionAct,
}

type actRequest struct {
	Kind        string           `json:"kind,omitempty"`
	TargetID    string           `json:"targetId,omitempty"`
	Ref         string           `json:"ref,omitempty"`
	DoubleClick *bool            `json:"doubleClick,omitempty"`
	Button      string           `json:"button,omitempty"`
	Modifiers   []string         `json:"modifiers,omitempty"`
	Text        string           `json:"text,omitempty"`
	Submit      *bool            `json:"submit,omitempty"`
	Slowly      *bool            `json:"slowly,omitempty"`
	Key         string           `json:"key,omitempty"`
	DelayMs     *int             `json:"delayMs,omitempty"`
	StartRef    string           `json:"startRef,omitempty"`
	EndRef      string           `json:"endRef,omitempty"`
	Values      []string         `json:"values,omitempty"`
	Fields      []map[string]any `json:"fields,omitempty"`
	Width       *int             `json:"width,omitempty"`
	Height      *int             `json:"height,omitempty"`
	TimeMs      *int             `json:"timeMs,omitempty"`
	Selector    string           `json:"selector,omitempty"`
	URL         string           `json:"url,omitempty"`
	LoadState   string           `json:"loadState,omitempty"`
	TextGone    string           `json:"textGone,omitempty"`
	TimeoutMs   *int             `json:"timeoutMs,omitempty"`
	Fn          string           `json:"fn,omitempty"`
}

type input struct {
	Action         string            `json:"action"`
	Target         string            `json:"target,omitempty"`
	Node           string            `json:"node,omitempty"`
	Profile        string            `json:"profile,omitempty"`
	TargetURL      string            `json:"targetUrl,omitempty"`
	URL            string            `json:"url,omitempty"`
	TargetID       string            `json:"targetId,omitempty"`
	Operation      string            `json:"operation,omitempty"`
	Store          string            `json:"store,omitempty"`
	Value          string            `json:"value,omitempty"`
	Limit          *int              `json:"limit,omitempty"`
	MaxChars       *int              `json:"maxChars,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	SnapshotFormat string            `json:"snapshotFormat,omitempty"`
	Refs           string            `json:"refs,omitempty"`
	Interactive    *bool             `json:"interactive,omitempty"`
	Compact        *bool             `json:"compact,omitempty"`
	Depth          *int              `json:"depth,omitempty"`
	Selector       string            `json:"selector,omitempty"`
	Frame          string            `json:"frame,omitempty"`
	Labels         *bool             `json:"labels,omitempty"`
	FullPage       *bool             `json:"fullPage,omitempty"`
	Ref            string            `json:"ref,omitempty"`
	Element        string            `json:"element,omitempty"`
	Type           string            `json:"type,omitempty"`
	Level          string            `json:"level,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Offline        *bool             `json:"offline,omitempty"`
	Username       string            `json:"username,omitempty"`
	Password       string            `json:"password,omitempty"`
	Cookie         map[string]any    `json:"cookie,omitempty"`
	Path           string            `json:"path,omitempty"`
	Paths          []string          `json:"paths,omitempty"`
	InputRef       string            `json:"inputRef,omitempty"`
	Filename       string            `json:"filename,omitempty"`
	TimeoutMs      *int              `json:"timeoutMs,omitempty"`
	Clear          *bool             `json:"clear,omitempty"`
	Accept         *bool             `json:"accept,omitempty"`
	PromptText     string            `json:"promptText,omitempty"`
	Latitude       *float64          `json:"latitude,omitempty"`
	Longitude      *float64          `json:"longitude,omitempty"`
	Accuracy       *float64          `json:"accuracy,omitempty"`
	Origin         string            `json:"origin,omitempty"`
	ColorScheme    string            `json:"colorScheme,omitempty"`
	TimezoneID     string            `json:"timezoneId,omitempty"`
	Locale         string            `json:"locale,omitempty"`
	Name           string            `json:"name,omitempty"`
	Request        *actRequest       `json:"request,omitempty"`
	Kind           string            `json:"kind,omitempty"`
	DoubleClick    *bool             `json:"doubleClick,omitempty"`
	Button         string            `json:"button,omitempty"`
	Modifiers      []string          `json:"modifiers,omitempty"`
	Text           string            `json:"text,omitempty"`
	Submit         *bool             `json:"submit,omitempty"`
	Slowly         *bool             `json:"slowly,omitempty"`
	Key            string            `json:"key,omitempty"`
	DelayMs        *int              `json:"delayMs,omitempty"`
	StartRef       string            `json:"startRef,omitempty"`
	EndRef         string            `json:"endRef,omitempty"`
	Values         []string          `json:"values,omitempty"`
	Fields         []map[string]any  `json:"fields,omitempty"`
	Width          *int              `json:"width,omitempty"`
	Height         *int              `json:"height,omitempty"`
	TimeMs         *int              `json:"timeMs,omitempty"`
	TextGone       string            `json:"textGone,omitempty"`
	LoadState      string            `json:"loadState,omitempty"`
	Fn             string            `json:"fn,omitempty"`
}

// Tool implements the first-class browser tool contract.
type Tool struct {
	defaultProfile  string
	evaluateEnabled bool
	navigation      navigationPolicy
	hostServer      *serverTargetConfig
	sandboxServer   *serverTargetConfig
	nodeTargets     map[string]serverTargetConfig
	profiles        map[string]ProfileConfig
	drivers         map[string]driver
	serverDriversMu sync.RWMutex
	serverDrivers   map[string]driver
}

// NewTool creates a native browser tool backed by MCP or browser-server.
func NewTool(cfg Config) (*Tool, error) {
	resolved, err := resolveConfig(cfg)
	if err != nil {
		return nil, err
	}

	drivers := make(map[string]driver, len(resolved.Profiles))
	profiles := make(map[string]ProfileConfig, len(resolved.Profiles))
	for i := range resolved.Profiles {
		profile := resolved.Profiles[i]
		if profile.BrowserServerURL != "" {
			drivers[profile.Name] = newServerProfileDriver(
				profile.BrowserServerURL,
				profile.AuthToken,
				profile.Name,
			)
		} else if strings.TrimSpace(profile.Connection.Transport) != "" {
			drivers[profile.Name] = newMCPProfileDriver(profile)
		}
		profiles[profile.Name] = ProfileConfig{
			Name:             profile.Name,
			Description:      profile.Description,
			Transport:        profile.Connection.Transport,
			ServerURL:        profile.Connection.ServerURL,
			BrowserServerURL: profile.BrowserServerURL,
			AuthToken:        profile.AuthToken,
		}
	}

	return newToolWithDrivers(
		resolved.DefaultProfile,
		resolved.EvaluateEnabled,
		resolved.Navigation,
		resolved.HostServer,
		resolved.SandboxServer,
		resolved.NodeTargets,
		profiles,
		drivers,
	), nil
}

func newToolWithDrivers(
	defaultProfile string,
	evaluateEnabled bool,
	navigation navigationPolicy,
	hostServer *serverTargetConfig,
	sandboxServer *serverTargetConfig,
	nodeTargets map[string]serverTargetConfig,
	profiles map[string]ProfileConfig,
	drivers map[string]driver,
) *Tool {
	return &Tool{
		defaultProfile:  defaultProfile,
		evaluateEnabled: evaluateEnabled,
		navigation:      navigation,
		hostServer:      hostServer,
		sandboxServer:   sandboxServer,
		nodeTargets:     nodeTargets,
		profiles:        profiles,
		drivers:         drivers,
		serverDrivers:   make(map[string]driver),
	}
}

func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: ToolName,
		Description: "Control a real browser through OpenClaw's " +
			"native browser contract. Prefer snapshot + act for UI " +
			"automation. Keep using the same targetId after tabs or " +
			"snapshot calls. Use profile=\"chrome\" when the user " +
			"mentions a browser extension, relay, attach tab, or " +
			"their current browser tab. Use target=\"sandbox\" or " +
			"target=\"node\" when the runtime exposes those browser " +
			"servers. Avoid evaluate unless the " +
			"task truly requires custom page JavaScript.",
		InputSchema: browserSchema(),
	}
}

func (t *Tool) Call(ctx context.Context, args []byte) (any, error) {
	var in input
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid browser args: %w", err)
	}

	action := strings.TrimSpace(in.Action)
	if action == "" {
		return nil, errors.New("browser action is required")
	}
	actionKey := strings.ToLower(action)
	if err := validateTargetSelection(in); err != nil {
		return nil, err
	}

	switch actionKey {
	case strings.ToLower(actionProfiles):
		return t.handleProfiles(ctx), nil
	case strings.ToLower(actionStatus):
		return t.handleStatus(ctx)
	}

	profileName, drv, err := t.resolveDriver(in)
	if err != nil {
		return nil, err
	}
	driverType := t.driverTypeForInput(profileName, in)

	switch actionKey {
	case strings.ToLower(actionStart):
		return t.handleStart(ctx, profileName, driverType, drv)
	case strings.ToLower(actionStop):
		return t.handleStop(profileName, driverType, drv)
	case strings.ToLower(actionTabs):
		return t.handleTabs(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionOpen):
		return t.handleOpen(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionFocus):
		return t.handleFocus(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionClose):
		return t.handleClose(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionSnapshot):
		return t.handleSnapshot(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionScreenshot):
		return t.handleScreenshot(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionNavigate):
		return t.handleNavigate(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionConsole):
		return t.handleConsole(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionCookies):
		return t.handleCookies(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionStorage):
		return t.handleStorage(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionPDF):
		return t.handlePDF(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionDownload):
		return t.handleDownload(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionWaitDL):
		return t.handleWaitDownload(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionUpload):
		return t.handleUpload(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionDialog):
		return t.handleDialog(ctx, profileName, driverType, drv, in)
	case strings.ToLower(actionOffline):
		return t.handleOffline(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionHeaders):
		return t.handleHeaders(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionCreds):
		return t.handleCredentials(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionGeo):
		return t.handleGeolocation(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionMedia):
		return t.handleMedia(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionTimezone):
		return t.handleTimezone(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionLocale):
		return t.handleLocale(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionDevice):
		return t.handleDevice(
			ctx,
			profileName,
			driverType,
			drv,
			in,
		)
	case strings.ToLower(actionAct):
		return t.handleAct(ctx, profileName, driverType, drv, in)
	default:
		return nil, fmt.Errorf(
			"unsupported browser action %q",
			in.Action,
		)
	}
}

func browserSchema() *tool.Schema {
	requestProps := map[string]*tool.Schema{
		"kind":        stringSchema("Browser act kind."),
		"targetId":    stringSchema("Tab target id from tabs output."),
		"ref":         stringSchema("Snapshot ref id."),
		"doubleClick": boolSchema("Double click."),
		"button":      stringSchema("Mouse button for click."),
		"modifiers":   stringArraySchema("Optional key modifiers."),
		"text":        stringSchema("Text input."),
		"submit":      boolSchema("Submit after typing."),
		"slowly":      boolSchema("Type slowly."),
		"key":         stringSchema("Keyboard key."),
		"delayMs":     numberSchema("Key delay."),
		"startRef":    stringSchema("Drag start ref."),
		"endRef":      stringSchema("Drag end ref."),
		"values":      stringArraySchema("Selected option values."),
		"fields": {
			Type: "array",
			Items: &tool.Schema{
				Type:                 "object",
				AdditionalProperties: true,
			},
		},
		"width":     numberSchema("Viewport width."),
		"height":    numberSchema("Viewport height."),
		"timeMs":    numberSchema("Wait duration in milliseconds."),
		"selector":  stringSchema("Selector for wait."),
		"url":       stringSchema("URL for wait."),
		"loadState": stringSchema("Load state for wait."),
		"textGone":  stringSchema("Text that must disappear."),
		"timeoutMs": numberSchema("Timeout in milliseconds."),
		"fn":        stringSchema("Page function for evaluate."),
	}

	properties := map[string]*tool.Schema{
		"action":         stringSchema("Browser action."),
		"target":         stringSchema("Browser target: host, sandbox, or node."),
		"node":           stringSchema("Node browser target."),
		"profile":        stringSchema("Browser profile name."),
		"targetUrl":      stringSchema("Alias for open URL."),
		"url":            stringSchema("Browser URL."),
		"targetId":       stringSchema("Tab target id."),
		"operation":      stringSchema("State operation."),
		"store":          stringSchema("Storage scope."),
		"value":          stringSchema("State value."),
		"limit":          numberSchema("Tab list limit."),
		"maxChars":       numberSchema("Max untrusted text chars."),
		"mode":           stringSchema("Snapshot mode."),
		"snapshotFormat": stringSchema("Snapshot format."),
		"refs":           stringSchema("Snapshot refs mode."),
		"interactive":    boolSchema("Interactive snapshot."),
		"compact":        boolSchema("Compact snapshot."),
		"depth":          numberSchema("Snapshot depth."),
		"selector":       stringSchema("Wait selector."),
		"frame":          stringSchema("Frame id."),
		"labels":         boolSchema("Include labels."),
		"fullPage":       boolSchema("Capture full page."),
		"ref":            stringSchema("Snapshot ref."),
		"element":        stringSchema("Element description."),
		"type":           stringSchema("Image type."),
		"level":          stringSchema("Console level."),
		"headers": {
			Type: "object",
			AdditionalProperties: &tool.Schema{
				Type: "string",
			},
		},
		"offline":  boolSchema("Offline mode."),
		"username": stringSchema("HTTP auth username."),
		"password": stringSchema("HTTP auth password."),
		"cookie": {
			Type:                 "object",
			AdditionalProperties: true,
		},
		"path":        stringSchema("Download output path."),
		"paths":       stringArraySchema("Upload paths."),
		"inputRef":    stringSchema("Upload input ref."),
		"filename":    stringSchema("Optional output filename."),
		"timeoutMs":   numberSchema("Timeout in milliseconds."),
		"clear":       boolSchema("Clear existing override."),
		"accept":      boolSchema("Dialog accept flag."),
		"promptText":  stringSchema("Dialog prompt text."),
		"latitude":    numberSchema("Geolocation latitude."),
		"longitude":   numberSchema("Geolocation longitude."),
		"accuracy":    numberSchema("Geolocation accuracy."),
		"origin":      stringSchema("Permission origin."),
		"colorScheme": stringSchema("Color scheme."),
		"timezoneId":  stringSchema("Timezone override."),
		"locale":      stringSchema("Locale override."),
		"name":        stringSchema("Device name."),
		"kind":        stringSchema("Legacy act kind."),
		"doubleClick": boolSchema("Double click."),
		"button":      stringSchema("Mouse button."),
		"modifiers":   stringArraySchema("Key modifiers."),
		"text":        stringSchema("Input text."),
		"submit":      boolSchema("Submit after typing."),
		"slowly":      boolSchema("Type slowly."),
		"key":         stringSchema("Keyboard or state key."),
		"delayMs":     numberSchema("Key delay."),
		"startRef":    stringSchema("Drag start ref."),
		"endRef":      stringSchema("Drag end ref."),
		"values":      stringArraySchema("Selected option values."),
		"fields": {
			Type: "array",
			Items: &tool.Schema{
				Type:                 "object",
				AdditionalProperties: true,
			},
		},
		"width":     numberSchema("Viewport width."),
		"height":    numberSchema("Viewport height."),
		"timeMs":    numberSchema("Wait duration."),
		"textGone":  stringSchema("Text that must disappear."),
		"loadState": stringSchema("Wait load state."),
		"fn":        stringSchema("Evaluate function."),
		"request": {
			Type:       "object",
			Properties: requestProps,
		},
	}

	return &tool.Schema{
		Type:       "object",
		Required:   []string{"action"},
		Properties: properties,
	}
}

func stringSchema(desc string) *tool.Schema {
	return &tool.Schema{Type: "string", Description: desc}
}

func numberSchema(desc string) *tool.Schema {
	return &tool.Schema{Type: "number", Description: desc}
}

func boolSchema(desc string) *tool.Schema {
	return &tool.Schema{Type: "boolean", Description: desc}
}

func stringArraySchema(desc string) *tool.Schema {
	return &tool.Schema{
		Type:        "array",
		Description: desc,
		Items:       &tool.Schema{Type: "string"},
	}
}

func validateTargetSelection(in input) error {
	target := strings.ToLower(strings.TrimSpace(in.Target))
	switch target {
	case "", targetHost:
	case targetSandbox, targetNode:
	default:
		return fmt.Errorf("unknown browser target %q", in.Target)
	}

	if strings.TrimSpace(in.Node) != "" && target != targetNode {
		return errors.New(
			"browser node is only valid with target=node",
		)
	}
	return nil
}

func (t *Tool) resolveDriver(
	in input,
) (string, driver, error) {
	profile := strings.TrimSpace(in.Profile)
	if profile == "" {
		profile = t.defaultProfile
	}

	targetName := strings.ToLower(strings.TrimSpace(in.Target))
	switch targetName {
	case "", targetHost:
		if drv, ok := t.serverDriverForTarget(t.hostServer, profile); ok {
			return profile, drv, nil
		}
	case targetSandbox:
		if drv, ok := t.serverDriverForTarget(t.sandboxServer, profile); ok {
			return profile, drv, nil
		}
		return "", nil, errors.New(
			"browser sandbox target is not configured",
		)
	case targetNode:
		nodeID := strings.TrimSpace(in.Node)
		if nodeID == "" {
			if len(t.nodeTargets) == 1 {
				for id := range t.nodeTargets {
					nodeID = id
				}
			} else {
				return "", nil, errors.New(
					"browser target=node requires node",
				)
			}
		}
		target, ok := t.nodeTargets[nodeID]
		if !ok {
			return "", nil, fmt.Errorf(
				"browser node %q is not configured",
				nodeID,
			)
		}
		if drv, ok := t.serverDriverForTarget(&target, profile); ok {
			return profile, drv, nil
		}
		return "", nil, fmt.Errorf(
			"browser node %q has no server url",
			nodeID,
		)
	default:
		return "", nil, fmt.Errorf(
			"unknown browser target %q",
			in.Target,
		)
	}

	drv, ok := t.drivers[profile]
	if !ok {
		return "", nil, fmt.Errorf(
			"browser profile %q is not configured",
			profile,
		)
	}
	return profile, drv, nil
}

func (t *Tool) serverDriverForTarget(
	target *serverTargetConfig,
	profile string,
) (driver, bool) {
	if target == nil || strings.TrimSpace(target.ServerURL) == "" {
		return nil, false
	}
	key := target.ServerURL + "\x00" + target.AuthToken + "\x00" + profile
	t.serverDriversMu.RLock()
	drv, ok := t.serverDrivers[key]
	t.serverDriversMu.RUnlock()
	if ok {
		return drv, true
	}

	created := newServerProfileDriver(
		target.ServerURL,
		target.AuthToken,
		profile,
	)
	t.serverDriversMu.Lock()
	defer t.serverDriversMu.Unlock()
	if drv, ok = t.serverDrivers[key]; ok {
		return drv, true
	}
	t.serverDrivers[key] = created
	return created, true
}

func (t *Tool) handleProfiles(
	ctx context.Context,
) Result {
	names := make([]string, 0, len(t.profiles))
	for name := range t.profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	out := Result{
		Action:          actionProfiles,
		DefaultProfile:  t.defaultProfile,
		Driver:          ToolName,
		EvaluateEnabled: t.evaluateEnabled,
		Supported:       append([]string(nil), supportedActions...),
		Profiles:        make([]ProfileInfo, 0, len(t.profiles)),
	}

	for _, name := range names {
		cfg := t.profiles[name]
		info := ProfileInfo{
			Name:        name,
			Description: cfg.Description,
			Default:     name == t.defaultProfile,
			Driver:      t.driverTypeForProfile(name),
			Supported:   append([]string(nil), supportedActions...),
		}
		drv := t.statusDriver(name, cfg)
		if drv != nil {
			status, err := drv.Status(ctx)
			if err == nil {
				info.State = status.State
				info.ToolCount = status.ToolCount
			} else {
				info.State = stateStopped
			}
		}
		out.Profiles = append(out.Profiles, info)
	}
	return out
}

func (t *Tool) statusDriver(
	name string,
	cfg ProfileConfig,
) driver {
	if strings.TrimSpace(cfg.BrowserServerURL) != "" {
		return t.drivers[name]
	}
	if drv, ok := t.serverDriverForTarget(t.hostServer, name); ok {
		return drv
	}
	return t.drivers[name]
}

func (t *Tool) driverTypeForProfile(
	profile string,
) string {
	cfg, ok := t.profiles[profile]
	if !ok {
		if t.hostServer != nil {
			return driverTypeBrowserServer
		}
		return driverTypePlaywrightMCP
	}
	if strings.TrimSpace(cfg.BrowserServerURL) != "" {
		return driverTypeBrowserServer
	}
	if strings.TrimSpace(cfg.Transport) != "" {
		return driverTypePlaywrightMCP
	}
	if t.hostServer != nil || t.sandboxServer != nil ||
		len(t.nodeTargets) > 0 {
		return driverTypeBrowserServer
	}
	return driverTypePlaywrightMCP
}

func (t *Tool) driverTypeForInput(
	profile string,
	in input,
) string {
	target := strings.ToLower(strings.TrimSpace(in.Target))
	switch target {
	case targetSandbox, targetNode:
		return driverTypeBrowserServer
	case "", targetHost:
		if t.hostServer != nil {
			return driverTypeBrowserServer
		}
	}
	return t.driverTypeForProfile(profile)
}

func (t *Tool) handleStatus(
	ctx context.Context,
) (Result, error) {
	result := t.handleProfiles(ctx)
	result.Action = actionStatus
	return result, nil
}

func (t *Tool) handleStart(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
) (Result, error) {
	status, err := drv.Start(ctx)
	if err != nil {
		return Result{}, err
	}

	result := newBaseResult(
		actionStart,
		profile,
		driverType,
		t.evaluateEnabled,
	)
	result.State = status.State
	result.ToolCount = status.ToolCount
	return result, nil
}

func (t *Tool) handleStop(
	profile string,
	driverType string,
	drv driver,
) (Result, error) {
	if err := drv.Stop(); err != nil {
		return Result{}, err
	}

	result := newBaseResult(
		actionStop,
		profile,
		driverType,
		t.evaluateEnabled,
	)
	result.State = stateStopped
	return result, nil
}

func (t *Tool) handleTabs(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	raw, err := drv.Call(ctx, mcpToolTabs, map[string]any{
		"action": tabActionList,
	})
	if err != nil {
		return Result{}, err
	}
	return t.tabsResult(profile, driverType, raw, in.Limit), nil
}

func (t *Tool) handleOpen(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	rawURL := strings.TrimSpace(in.URL)
	if rawURL == "" {
		rawURL = strings.TrimSpace(in.TargetURL)
	}
	if err := t.navigation.Validate(rawURL); err != nil {
		return Result{}, err
	}
	args := map[string]any{
		"action": tabActionNew,
	}

	if _, err := drv.Call(ctx, mcpToolTabs, args); err != nil {
		return Result{}, err
	}
	if rawURL != "" {
		if _, err := drv.Call(ctx, mcpToolNavigate, map[string]any{
			"url": rawURL,
		}); err != nil {
			return Result{}, err
		}
	}
	result, err := t.handleTabs(
		ctx,
		profile,
		driverType,
		drv,
		input{},
	)
	if err != nil {
		return Result{}, err
	}
	result.Action = actionOpen
	return result, nil
}

func (t *Tool) handleFocus(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	index, err := parseTargetID(in.TargetID)
	if err != nil {
		return Result{}, err
	}
	if _, err := drv.Call(ctx, mcpToolTabs, map[string]any{
		"action": tabActionSelect,
		"index":  index,
	}); err != nil {
		return Result{}, err
	}
	result, err := t.handleTabs(
		ctx,
		profile,
		driverType,
		drv,
		input{},
	)
	if err != nil {
		return Result{}, err
	}
	result.Action = actionFocus
	return result, nil
}

func (t *Tool) handleClose(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	args := map[string]any{"action": tabActionClose}
	if strings.TrimSpace(in.TargetID) != "" {
		index, err := parseTargetID(in.TargetID)
		if err != nil {
			return Result{}, err
		}
		args["index"] = index
	}
	if _, err := drv.Call(ctx, mcpToolTabs, args); err != nil {
		return Result{}, err
	}
	result, err := t.handleTabs(
		ctx,
		profile,
		driverType,
		drv,
		input{},
	)
	if err != nil {
		return Result{}, err
	}
	result.Action = actionClose
	return result, nil
}

func (t *Tool) handleSnapshot(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}

	args := map[string]any{}
	if filename := strings.TrimSpace(in.Filename); filename != "" {
		args["filename"] = filename
	}
	if driverType != driverTypeBrowserServer &&
		hasServerSnapshotArgs(in) {
		return Result{}, errors.New(
			"advanced snapshot options are only supported by " +
				"the browser-server driver",
		)
	}
	if driverType == driverTypeBrowserServer {
		addServerSnapshotArgs(args, in)
	}
	raw, err := drv.Call(ctx, mcpToolSnapshot, args)
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionSnapshot,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleScreenshot(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}

	args := map[string]any{}
	if in.FullPage != nil {
		args["fullPage"] = *in.FullPage
	}
	if filename := strings.TrimSpace(in.Filename); filename != "" {
		args["filename"] = filename
	}
	if ref := strings.TrimSpace(in.Ref); ref != "" {
		args["ref"] = ref
	}
	if element := strings.TrimSpace(in.Element); element != "" {
		args["element"] = element
	}
	if imageType := strings.TrimSpace(in.Type); imageType != "" {
		args["type"] = imageType
	}

	raw, err := drv.Call(ctx, mcpToolScreenshot, args)
	if err != nil {
		return Result{}, err
	}

	result := newBaseResult(
		actionScreenshot,
		profile,
		driverType,
		t.evaluateEnabled,
	)
	result.TargetID = strings.TrimSpace(in.TargetID)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleNavigate(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}

	rawURL := strings.TrimSpace(in.URL)
	if rawURL == "" {
		return Result{}, errors.New("browser navigate requires url")
	}
	if err := t.navigation.Validate(rawURL); err != nil {
		return Result{}, err
	}

	raw, err := drv.Call(ctx, mcpToolNavigate, map[string]any{
		"url": rawURL,
	})
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionNavigate,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleConsole(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}

	args := map[string]any{}
	if level := strings.TrimSpace(in.Level); level != "" {
		args["level"] = level
	}
	if filename := strings.TrimSpace(in.Filename); filename != "" {
		args["filename"] = filename
	}
	raw, err := drv.Call(ctx, mcpToolConsole, args)
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionConsole,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleCookies(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser cookies are only supported by the " +
				"browser-server driver",
		)
	}

	op := stateOperation(in.Operation)
	args := map[string]any{}
	toolName := mcpToolCookies
	switch op {
	case "", stateOpGet:
	case stateOpSet:
		if len(in.Cookie) == 0 {
			return Result{}, errors.New(
				"browser cookies set requires cookie",
			)
		}
		toolName = mcpToolCookiesSet
		args["cookie"] = in.Cookie
	case stateOpClear:
		toolName = mcpToolCookiesClear
	default:
		return Result{}, fmt.Errorf(
			"unsupported cookies operation %q",
			in.Operation,
		)
	}

	raw, err := drv.Call(ctx, toolName, args)
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionCookies,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleStorage(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser storage is only supported by the " +
				"browser-server driver",
		)
	}

	scope := storageScope(in.Store)
	args := map[string]any{
		"kind": scope,
	}
	if key := strings.TrimSpace(in.Key); key != "" {
		args["key"] = key
	}

	op := stateOperation(in.Operation)
	toolName := mcpToolStorageGet
	switch op {
	case "", stateOpGet:
	case stateOpSet:
		if strings.TrimSpace(in.Key) == "" {
			return Result{}, errors.New(
				"browser storage set requires key",
			)
		}
		toolName = mcpToolStorageSet
		args["value"] = in.Value
	case stateOpClear:
		toolName = mcpToolStorageClear
	default:
		return Result{}, fmt.Errorf(
			"unsupported storage operation %q",
			in.Operation,
		)
	}

	raw, err := drv.Call(ctx, toolName, args)
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionStorage,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleOffline(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser offline is only supported by the " +
				"browser-server driver",
		)
	}
	if in.Offline == nil {
		return Result{}, errors.New("browser offline requires offline")
	}

	raw, err := drv.Call(ctx, mcpToolSetOffline, map[string]any{
		"offline": *in.Offline,
	})
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionOffline,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleHeaders(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser headers are only supported by the " +
				"browser-server driver",
		)
	}

	headers := map[string]string{}
	for key, value := range in.Headers {
		headers[key] = value
	}
	raw, err := drv.Call(ctx, mcpToolSetHeaders, map[string]any{
		"headers": headers,
	})
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionHeaders,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleCredentials(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser credentials are only supported by the " +
				"browser-server driver",
		)
	}

	args := map[string]any{}
	if boolValue(in.Clear) {
		args["clear"] = true
	} else {
		username := strings.TrimSpace(in.Username)
		if username == "" {
			return Result{}, errors.New(
				"browser credentials requires username or clear",
			)
		}
		args["username"] = username
		args["password"] = in.Password
	}

	raw, err := drv.Call(ctx, mcpToolSetCreds, args)
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionCreds,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleGeolocation(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser geolocation is only supported by the " +
				"browser-server driver",
		)
	}

	args := map[string]any{}
	if boolValue(in.Clear) {
		args["clear"] = true
	} else {
		if in.Latitude == nil || in.Longitude == nil {
			return Result{}, errors.New(
				"browser geolocation requires latitude and longitude",
			)
		}
		args["latitude"] = *in.Latitude
		args["longitude"] = *in.Longitude
		if in.Accuracy != nil {
			args["accuracy"] = *in.Accuracy
		}
	}
	if origin := strings.TrimSpace(in.Origin); origin != "" {
		args["origin"] = origin
	}

	raw, err := drv.Call(ctx, mcpToolSetGeo, args)
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionGeo,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleMedia(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser media is only supported by the " +
				"browser-server driver",
		)
	}
	colorScheme := strings.TrimSpace(in.ColorScheme)
	if colorScheme == "" {
		return Result{}, errors.New("browser media requires colorScheme")
	}

	raw, err := drv.Call(ctx, mcpToolSetMedia, map[string]any{
		"colorScheme": colorScheme,
	})
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionMedia,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleTimezone(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser timezone is only supported by the " +
				"browser-server driver",
		)
	}
	timezoneID := strings.TrimSpace(in.TimezoneID)
	if timezoneID == "" {
		return Result{}, errors.New(
			"browser timezone requires timezoneId",
		)
	}

	raw, err := drv.Call(ctx, mcpToolSetTZ, map[string]any{
		"timezoneId": timezoneID,
	})
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionTimezone,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleLocale(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser locale is only supported by the " +
				"browser-server driver",
		)
	}
	locale := strings.TrimSpace(in.Locale)
	if locale == "" {
		return Result{}, errors.New("browser locale requires locale")
	}

	raw, err := drv.Call(ctx, mcpToolSetLocale, map[string]any{
		"locale": locale,
	})
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionLocale,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handleDevice(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser device is only supported by the " +
				"browser-server driver",
		)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Result{}, errors.New("browser device requires name")
	}

	raw, err := drv.Call(ctx, mcpToolSetDevice, map[string]any{
		"name": name,
	})
	if err != nil {
		return Result{}, err
	}
	result := t.textResult(
		actionDevice,
		profile,
		driverType,
		in.MaxChars,
		raw,
	)
	result.Content = raw
	return result, nil
}

func (t *Tool) handlePDF(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}

	args := map[string]any{}
	if filename := strings.TrimSpace(in.Filename); filename != "" {
		args["filename"] = filename
	}
	raw, err := drv.Call(ctx, mcpToolPDF, args)
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionPDF,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleDownload(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser download is only supported by the " +
				"browser-server driver",
		)
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" {
		return Result{}, errors.New("browser download requires ref")
	}

	args := map[string]any{
		"ref": ref,
	}
	if outputPath := downloadOutputPath(in); outputPath != "" {
		args["path"] = outputPath
	}
	if timeout := intValue(in.TimeoutMs); timeout > 0 {
		args["timeoutMs"] = timeout
	}

	raw, err := drv.Call(ctx, mcpToolDownload, args)
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionDownload,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleWaitDownload(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if driverType != driverTypeBrowserServer {
		return Result{}, errors.New(
			"browser waitDownload is only supported by the " +
				"browser-server driver",
		)
	}

	args := map[string]any{}
	if outputPath := downloadOutputPath(in); outputPath != "" {
		args["path"] = outputPath
	}
	if timeout := intValue(in.TimeoutMs); timeout > 0 {
		args["timeoutMs"] = timeout
	}

	raw, err := drv.Call(ctx, mcpToolWaitDownload, args)
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionWaitDL,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleUpload(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}
	if len(in.Paths) == 0 {
		return Result{}, errors.New("browser upload requires paths")
	}

	args := map[string]any{
		"paths": in.Paths,
	}
	if inputRef := strings.TrimSpace(in.InputRef); inputRef != "" {
		args["inputRef"] = inputRef
	}
	if driverType != driverTypeBrowserServer &&
		(strings.TrimSpace(in.Ref) != "" ||
			strings.TrimSpace(in.Element) != "") {
		return Result{}, errors.New(
			"browser upload with ref or element is only " +
				"supported by the browser-server driver",
		)
	}
	if driverType == driverTypeBrowserServer {
		if ref := strings.TrimSpace(in.Ref); ref != "" {
			args["ref"] = ref
		}
		if element := strings.TrimSpace(in.Element); element != "" {
			args["element"] = element
		}
		if timeout := intValue(in.TimeoutMs); timeout > 0 {
			args["timeoutMs"] = timeout
		}
	}

	raw, err := drv.Call(ctx, mcpToolUpload, args)
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionUpload,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleDialog(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	if err := selectTarget(ctx, drv, in.TargetID); err != nil {
		return Result{}, err
	}

	args := map[string]any{}
	if in.Accept != nil {
		args["accept"] = *in.Accept
	}
	if prompt := strings.TrimSpace(in.PromptText); prompt != "" {
		args["promptText"] = prompt
	}

	raw, err := drv.Call(ctx, mcpToolDialog, args)
	if err != nil {
		return Result{}, err
	}
	return t.textResult(
		actionDialog,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func (t *Tool) handleAct(
	ctx context.Context,
	profile string,
	driverType string,
	drv driver,
	in input,
) (Result, error) {
	req := normalizeActRequest(in)
	if err := selectTarget(ctx, drv, req.TargetID); err != nil {
		return Result{}, err
	}

	raw, err := t.executeAct(ctx, drv, req, driverType)
	if err != nil {
		return Result{}, err
	}

	if req.Kind == actClose {
		result, err := t.handleTabs(
			ctx,
			profile,
			driverType,
			drv,
			input{},
		)
		if err != nil {
			return Result{}, err
		}
		result.Action = actionAct
		return result, nil
	}
	return t.textResult(
		actionAct,
		profile,
		driverType,
		in.MaxChars,
		raw,
	), nil
}

func normalizeActRequest(in input) actRequest {
	if in.Request != nil {
		req := *in.Request
		if strings.TrimSpace(req.TargetID) == "" {
			req.TargetID = in.TargetID
		}
		if strings.TrimSpace(req.Ref) == "" {
			req.Ref = in.Ref
		}
		return req
	}

	return actRequest{
		Kind:        in.Kind,
		TargetID:    in.TargetID,
		Ref:         in.Ref,
		DoubleClick: in.DoubleClick,
		Button:      in.Button,
		Modifiers:   in.Modifiers,
		Text:        in.Text,
		Submit:      in.Submit,
		Slowly:      in.Slowly,
		Key:         in.Key,
		DelayMs:     in.DelayMs,
		StartRef:    in.StartRef,
		EndRef:      in.EndRef,
		Values:      in.Values,
		Fields:      in.Fields,
		Width:       in.Width,
		Height:      in.Height,
		TimeMs:      in.TimeMs,
		Selector:    in.Selector,
		URL:         in.URL,
		LoadState:   in.LoadState,
		TextGone:    in.TextGone,
		TimeoutMs:   in.TimeoutMs,
		Fn:          in.Fn,
	}
}

func (t *Tool) executeAct(
	ctx context.Context,
	drv driver,
	req actRequest,
	driverType string,
) (any, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case actClick:
		args := map[string]any{
			"ref":         strings.TrimSpace(req.Ref),
			"element":     describeElement(req.Ref, ""),
			"button":      strings.TrimSpace(req.Button),
			"doubleClick": boolValue(req.DoubleClick),
			"modifiers":   req.Modifiers,
		}
		addServerTimeoutArg(args, driverType, req.TimeoutMs)
		return drv.Call(ctx, mcpToolClick, args)
	case actType:
		args := map[string]any{
			"ref":     strings.TrimSpace(req.Ref),
			"element": describeElement(req.Ref, ""),
			"text":    req.Text,
			"submit":  boolValue(req.Submit),
			"slowly":  boolValue(req.Slowly),
		}
		addServerTimeoutArg(args, driverType, req.TimeoutMs)
		return drv.Call(ctx, mcpToolType, args)
	case actPress:
		args := map[string]any{
			"key": strings.TrimSpace(req.Key),
		}
		if delay := intValue(req.DelayMs); delay > 0 {
			args["delayMs"] = delay
		}
		return drv.Call(ctx, mcpToolPressKey, args)
	case actHover:
		args := map[string]any{
			"ref":     strings.TrimSpace(req.Ref),
			"element": describeElement(req.Ref, ""),
		}
		addServerTimeoutArg(args, driverType, req.TimeoutMs)
		return drv.Call(ctx, mcpToolHover, args)
	case strings.ToLower(actScrollIntoView):
		if driverType != driverTypeBrowserServer {
			return nil, errors.New(
				"scrollIntoView is only supported by the " +
					"browser-server driver",
			)
		}
		args := map[string]any{
			"ref": strings.TrimSpace(req.Ref),
		}
		addServerTimeoutArg(args, driverType, req.TimeoutMs)
		return drv.Call(ctx, mcpToolScroll, args)
	case actDrag:
		args := map[string]any{
			"startRef":     strings.TrimSpace(req.StartRef),
			"startElement": describeElement(req.StartRef, "start"),
			"endRef":       strings.TrimSpace(req.EndRef),
			"endElement":   describeElement(req.EndRef, "end"),
		}
		addServerTimeoutArg(args, driverType, req.TimeoutMs)
		return drv.Call(ctx, mcpToolDrag, args)
	case actSelect:
		args := map[string]any{
			"ref":     strings.TrimSpace(req.Ref),
			"element": describeElement(req.Ref, ""),
			"values":  req.Values,
		}
		addServerTimeoutArg(args, driverType, req.TimeoutMs)
		return drv.Call(ctx, mcpToolSelect, args)
	case actFill:
		return t.executeFill(ctx, drv, req, driverType)
	case actResize:
		return drv.Call(ctx, mcpToolResize, map[string]any{
			"width":  intValue(req.Width),
			"height": intValue(req.Height),
		})
	case actWait:
		return t.executeWait(ctx, drv, req, driverType)
	case actEvaluate:
		if !t.evaluateEnabled {
			return nil, errors.New(
				"browser evaluate is disabled by config",
			)
		}
		return drv.Call(ctx, mcpToolEvaluate, map[string]any{
			"function": strings.TrimSpace(req.Fn),
			"ref":      strings.TrimSpace(req.Ref),
			"element":  describeElement(req.Ref, ""),
		})
	case actClose:
		return drv.Call(ctx, mcpToolTabs, map[string]any{
			"action": tabActionClose,
		})
	default:
		return nil, fmt.Errorf(
			"unsupported browser act kind %q",
			req.Kind,
		)
	}
}

func (t *Tool) executeFill(
	ctx context.Context,
	drv driver,
	req actRequest,
	driverType string,
) (any, error) {
	if len(req.Fields) == 0 {
		return nil, errors.New("browser fill requires fields")
	}
	args := map[string]any{
		"fields": req.Fields,
	}
	addServerTimeoutArg(args, driverType, req.TimeoutMs)
	return drv.Call(ctx, mcpToolFillForm, args)
}

func (t *Tool) executeWait(
	ctx context.Context,
	drv driver,
	req actRequest,
	driverType string,
) (any, error) {
	supportsExtendedWait := driverType == driverTypeBrowserServer
	fn := strings.TrimSpace(req.Fn)
	if fn != "" && !t.evaluateEnabled {
		return nil, errors.New(
			"browser evaluate is disabled by config",
		)
	}
	if !supportsExtendedWait && (strings.TrimSpace(req.URL) != "" ||
		strings.TrimSpace(req.LoadState) != "" ||
		strings.TrimSpace(req.Selector) != "" ||
		fn != "") {
		return nil, errors.New(
			"wait with url, loadState, selector, or fn " +
				"is not supported by the current browser " +
				"driver",
		)
	}

	args := map[string]any{}
	if selector := strings.TrimSpace(req.Selector); selector != "" {
		args["selector"] = selector
	}
	if rawURL := strings.TrimSpace(req.URL); rawURL != "" {
		if err := t.navigation.Validate(rawURL); err != nil {
			return nil, err
		}
		args["url"] = rawURL
	}
	if loadState := strings.TrimSpace(req.LoadState); loadState != "" {
		args["loadState"] = loadState
	}
	if fn != "" {
		args["fn"] = fn
	}
	if req.TimeMs != nil {
		args["time"] = float64(*req.TimeMs) / 1000
	}
	if text := strings.TrimSpace(req.Text); text != "" {
		args["text"] = text
	}
	if textGone := strings.TrimSpace(req.TextGone); textGone != "" {
		args["textGone"] = textGone
	}
	if timeout := intValue(req.TimeoutMs); timeout > 0 {
		args["timeoutMs"] = timeout
	}
	return drv.Call(ctx, mcpToolWait, args)
}

func (t *Tool) textResult(
	action string,
	profile string,
	driverType string,
	maxChars *int,
	raw any,
) Result {
	result := newBaseResult(
		action,
		profile,
		driverType,
		t.evaluateEnabled,
	)
	result.Untrusted = true
	result.Warning = untrustedBrowserWarning
	result.Text = wrapUntrustedText(
		extractText(raw),
		intValue(maxChars),
	)
	return result
}

func (t *Tool) tabsResult(
	profile string,
	driverType string,
	raw any,
	limit *int,
) Result {
	text := extractText(raw)
	result := newBaseResult(
		actionTabs,
		profile,
		driverType,
		t.evaluateEnabled,
	)
	result.Untrusted = true
	result.Warning = untrustedBrowserWarning
	result.Text = wrapUntrustedText(text, 0)
	result.Tabs = parseTabs(text)
	if limit != nil && *limit > 0 && len(result.Tabs) > *limit {
		result.Tabs = result.Tabs[:*limit]
	}
	for i := range result.Tabs {
		if result.Tabs[i].Active {
			result.TargetID = result.Tabs[i].TargetID
			break
		}
	}
	return result
}

func selectTarget(
	ctx context.Context,
	drv driver,
	targetID string,
) error {
	trimmed := strings.TrimSpace(targetID)
	if trimmed == "" {
		return nil
	}

	index, err := parseTargetID(trimmed)
	if err != nil {
		return err
	}
	_, err = drv.Call(ctx, mcpToolTabs, map[string]any{
		"action": tabActionSelect,
		"index":  index,
	})
	return err
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func stateOperation(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func storageScope(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "session":
		return "session"
	default:
		return "local"
	}
}

func addServerTimeoutArg(
	args map[string]any,
	driverType string,
	timeout *int,
) {
	if driverType != driverTypeBrowserServer {
		return
	}
	if value := intValue(timeout); value > 0 {
		args["timeoutMs"] = value
	}
}

func hasServerSnapshotArgs(in input) bool {
	return intValue(in.Limit) > 0 ||
		strings.TrimSpace(in.Mode) != "" ||
		strings.TrimSpace(in.SnapshotFormat) != "" ||
		strings.TrimSpace(in.Refs) != "" ||
		in.Interactive != nil ||
		in.Compact != nil ||
		intValue(in.Depth) > 0 ||
		strings.TrimSpace(in.Selector) != "" ||
		strings.TrimSpace(in.Frame) != "" ||
		in.Labels != nil
}

func addServerSnapshotArgs(args map[string]any, in input) {
	if limit := intValue(in.Limit); limit > 0 {
		args["limit"] = limit
	}
	if mode := strings.TrimSpace(in.Mode); mode != "" {
		args["mode"] = mode
	}
	if format := strings.TrimSpace(in.SnapshotFormat); format != "" {
		args["snapshotFormat"] = format
	}
	if refs := strings.TrimSpace(in.Refs); refs != "" {
		args["refs"] = refs
	}
	if in.Interactive != nil {
		args["interactive"] = *in.Interactive
	}
	if in.Compact != nil {
		args["compact"] = *in.Compact
	}
	if depth := intValue(in.Depth); depth > 0 {
		args["depth"] = depth
	}
	if selector := strings.TrimSpace(in.Selector); selector != "" {
		args["selector"] = selector
	}
	if frame := strings.TrimSpace(in.Frame); frame != "" {
		args["frame"] = frame
	}
	if in.Labels != nil {
		args["labels"] = *in.Labels
	}
}

func downloadOutputPath(in input) string {
	if outputPath := strings.TrimSpace(in.Path); outputPath != "" {
		return outputPath
	}
	return strings.TrimSpace(in.Filename)
}

func describeElement(ref string, fallback string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed != "" {
		return "element " + trimmed
	}
	if strings.TrimSpace(fallback) != "" {
		return "element " + fallback
	}
	return "element"
}
