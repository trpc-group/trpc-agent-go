//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	// ErrSessionNotActive indicates that no ACP session binding exists for the key.
	ErrSessionNotActive = errors.New("acpagent: session is not active")
	// ErrSessionConfigOptionNotFound indicates that the Agent did not advertise the option.
	ErrSessionConfigOptionNotFound = errors.New("acpagent: session config option not found")
	// ErrSessionConfigOptionTypeMismatch indicates that the setter type does not match the option.
	ErrSessionConfigOptionTypeMismatch = errors.New("acpagent: session config option type mismatch")
	// ErrSessionConfigValueNotAllowed indicates that a select value was not advertised.
	ErrSessionConfigValueNotAllowed = errors.New("acpagent: session config value is not allowed")
)

// SessionConfigOptionCategory is the optional semantic category of a session config option.
type SessionConfigOptionCategory string

const (
	// SessionConfigOptionCategoryMode identifies an Agent operating mode selector.
	SessionConfigOptionCategoryMode SessionConfigOptionCategory = "mode"
	// SessionConfigOptionCategoryModel identifies a model selector.
	SessionConfigOptionCategoryModel SessionConfigOptionCategory = "model"
	// SessionConfigOptionCategoryModelConfig identifies a model configuration selector.
	SessionConfigOptionCategoryModelConfig SessionConfigOptionCategory = "model_config"
	// SessionConfigOptionCategoryThoughtLevel identifies a reasoning level selector.
	SessionConfigOptionCategoryThoughtLevel SessionConfigOptionCategory = "thought_level"
)

// SessionConfigSelectValue is one selectable value of a session config option.
type SessionConfigSelectValue struct {
	// Value is the stable identifier sent to the Agent.
	Value string
	// Name is the human-readable label for the value.
	Name string
	// Description is the optional value description.
	Description string
	// Group is the optional group identifier advertised by the Agent.
	Group string
	// GroupName is the human-readable label for Group.
	GroupName string
}

// SessionConfigSelectOption is a session config option with one selectable value.
type SessionConfigSelectOption struct {
	// ID is the stable config option identifier used by setters.
	ID string
	// Name is the human-readable label for the option.
	Name string
	// Description is the optional option description.
	Description string
	// Category is optional semantic metadata for presentation.
	Category SessionConfigOptionCategory
	// CurrentValue is the currently selected value identifier.
	CurrentValue string
	// Values contains the values currently allowed by the Agent.
	Values []SessionConfigSelectValue
	// Order is the zero-based position in the Agent's complete config option list.
	Order int
}

// SessionConfigBooleanOption is a boolean session config option.
type SessionConfigBooleanOption struct {
	// ID is the stable config option identifier used by setters.
	ID string
	// Name is the human-readable label for the option.
	Name string
	// Description is the optional option description.
	Description string
	// Category is optional semantic metadata for presentation.
	Category SessionConfigOptionCategory
	// CurrentValue is the current boolean value.
	CurrentValue bool
	// Order is the zero-based position in the Agent's complete config option list.
	Order int
}

type sessionConfigOptionKind uint8

const (
	sessionConfigOptionKindSelect sessionConfigOptionKind = iota
	sessionConfigOptionKindBoolean
)

func (k sessionConfigOptionKind) String() string {
	switch k {
	case sessionConfigOptionKindSelect:
		return "select"
	case sessionConfigOptionKindBoolean:
		return "boolean"
	default:
		return "unknown"
	}
}

type sessionConfigOption struct {
	id           string
	name         string
	description  string
	category     SessionConfigOptionCategory
	kind         sessionConfigOptionKind
	selectValue  string
	booleanValue bool
	values       []SessionConfigSelectValue
	order        int
}

type sessionConfiguration struct {
	options []sessionConfigOption
}

type sessionConfigSnapshot struct {
	SelectOptions  []SessionConfigSelectOption  `json:"selectOptions"`
	BooleanOptions []SessionConfigBooleanOption `json:"booleanOptions"`
}

func newSessionConfiguration(raw []acpsdk.SessionConfigOption) sessionConfiguration {
	config := sessionConfiguration{
		options: make([]sessionConfigOption, 0, len(raw)),
	}
	for index, rawOption := range raw {
		option, ok := newSessionConfigOption(rawOption, index)
		if ok {
			config.options = append(config.options, option)
		}
	}
	return config
}

func newSessionConfigOption(
	raw acpsdk.SessionConfigOption,
	order int,
) (sessionConfigOption, bool) {
	switch {
	case raw.Select != nil:
		option := sessionConfigOption{
			id:          string(raw.Select.Id),
			name:        raw.Select.Name,
			description: stringValue(raw.Select.Description),
			category:    configOptionCategory(raw.Select.Category),
			kind:        sessionConfigOptionKindSelect,
			selectValue: string(raw.Select.CurrentValue),
			order:       order,
		}
		option.values = selectValues(raw.Select.Options)
		return option, true
	case raw.Boolean != nil:
		return sessionConfigOption{
			id:           string(raw.Boolean.Id),
			name:         raw.Boolean.Name,
			description:  stringValue(raw.Boolean.Description),
			category:     configOptionCategory(raw.Boolean.Category),
			kind:         sessionConfigOptionKindBoolean,
			booleanValue: raw.Boolean.CurrentValue,
			order:        order,
		}, true
	default:
		return sessionConfigOption{}, false
	}
}

func configOptionCategory(
	category *acpsdk.SessionConfigOptionCategory,
) SessionConfigOptionCategory {
	if category == nil {
		return ""
	}
	return SessionConfigOptionCategory(*category)
}

func selectValues(options acpsdk.SessionConfigSelectOptions) []SessionConfigSelectValue {
	if options.Ungrouped != nil {
		return appendSelectValues(nil, "", "", *options.Ungrouped)
	}
	if options.Grouped == nil {
		return nil
	}
	var values []SessionConfigSelectValue
	for _, group := range *options.Grouped {
		values = appendSelectValues(
			values,
			string(group.Group),
			group.Name,
			group.Options,
		)
	}
	return values
}

func appendSelectValues(
	target []SessionConfigSelectValue,
	group string,
	groupName string,
	options []acpsdk.SessionConfigSelectOption,
) []SessionConfigSelectValue {
	for _, option := range options {
		target = append(target, SessionConfigSelectValue{
			Value:       string(option.Value),
			Name:        option.Name,
			Description: stringValue(option.Description),
			Group:       group,
			GroupName:   groupName,
		})
	}
	return target
}

func (c sessionConfiguration) clone() sessionConfiguration {
	cloned := sessionConfiguration{
		options: make([]sessionConfigOption, len(c.options)),
	}
	for index, option := range c.options {
		cloned.options[index] = option
		cloned.options[index].values = append(
			[]SessionConfigSelectValue(nil),
			option.values...,
		)
	}
	return cloned
}

func (c sessionConfiguration) selectOptions() []SessionConfigSelectOption {
	options := make([]SessionConfigSelectOption, 0, len(c.options))
	for _, option := range c.options {
		if option.kind != sessionConfigOptionKindSelect {
			continue
		}
		options = append(options, SessionConfigSelectOption{
			ID:           option.id,
			Name:         option.name,
			Description:  option.description,
			Category:     option.category,
			CurrentValue: option.selectValue,
			Values:       append([]SessionConfigSelectValue(nil), option.values...),
			Order:        option.order,
		})
	}
	return options
}

func (c sessionConfiguration) booleanOptions() []SessionConfigBooleanOption {
	options := make([]SessionConfigBooleanOption, 0, len(c.options))
	for _, option := range c.options {
		if option.kind != sessionConfigOptionKindBoolean {
			continue
		}
		options = append(options, SessionConfigBooleanOption{
			ID:           option.id,
			Name:         option.name,
			Description:  option.description,
			Category:     option.category,
			CurrentValue: option.booleanValue,
			Order:        option.order,
		})
	}
	return options
}

func (c sessionConfiguration) snapshot() sessionConfigSnapshot {
	return sessionConfigSnapshot{
		SelectOptions:  c.selectOptions(),
		BooleanOptions: c.booleanOptions(),
	}
}

func (c sessionConfiguration) find(id string) (sessionConfigOption, bool) {
	for _, option := range c.options {
		if option.id == id {
			return option, true
		}
	}
	return sessionConfigOption{}, false
}

func (o sessionConfigOption) allowsSelectValue(value string) bool {
	for _, candidate := range o.values {
		if candidate.Value == value {
			return true
		}
	}
	return false
}

func (s *acpSession) replaceSessionConfig(raw []acpsdk.SessionConfigOption) {
	if s == nil {
		return
	}
	config := newSessionConfiguration(raw)
	s.configMu.Lock()
	s.config = config
	s.configMu.Unlock()
}

func (s *acpSession) sessionConfigSnapshot() sessionConfiguration {
	if s == nil {
		return sessionConfiguration{}
	}
	s.configMu.RLock()
	config := s.config.clone()
	s.configMu.RUnlock()
	return config
}

// SessionConfigSelectOptions returns the select config options for an active ACP session.
//
// The boolean reports whether an ACP session binding exists. A true boolean with
// an empty slice means that the Agent did not advertise select options.
func (a *ACPAgent) SessionConfigSelectOptions(
	key session.Key,
) ([]SessionConfigSelectOption, bool) {
	if a == nil || key.CheckSessionKey() != nil {
		return nil, false
	}
	binding := a.sessions.lookup(key)
	if binding == nil {
		return nil, false
	}
	return binding.sessionConfigSnapshot().selectOptions(), true
}

// SessionConfigBooleanOptions returns the boolean config options for an active ACP session.
//
// The boolean reports whether an ACP session binding exists. A true boolean with
// an empty slice means that the Agent did not advertise boolean options.
func (a *ACPAgent) SessionConfigBooleanOptions(
	key session.Key,
) ([]SessionConfigBooleanOption, bool) {
	if a == nil || key.CheckSessionKey() != nil {
		return nil, false
	}
	binding := a.sessions.lookup(key)
	if binding == nil {
		return nil, false
	}
	return binding.sessionConfigSnapshot().booleanOptions(), true
}

// SetSessionConfigSelectOption sets a select value for an active ACP session.
//
// The call waits for an active Prompt to finish before applying the value. It
// returns ErrSessionNotActive when no binding exists, and returns the exported
// config validation errors when the option or value is not advertised.
func (a *ACPAgent) SetSessionConfigSelectOption(
	ctx context.Context,
	key session.Key,
	configID string,
	value string,
) error {
	return a.setSessionConfigOption(
		ctx,
		key,
		configID,
		sessionConfigOptionKindSelect,
		value,
		false,
	)
}

// SetSessionConfigBooleanOption sets a boolean value for an active ACP session.
//
// The call waits for an active Prompt to finish before applying the value. It
// returns ErrSessionNotActive when no binding exists, and returns the exported
// config validation errors when the option is not advertised or has another type.
func (a *ACPAgent) SetSessionConfigBooleanOption(
	ctx context.Context,
	key session.Key,
	configID string,
	value bool,
) error {
	return a.setSessionConfigOption(
		ctx,
		key,
		configID,
		sessionConfigOptionKindBoolean,
		"",
		value,
	)
}

func (a *ACPAgent) setSessionConfigOption(
	ctx context.Context,
	key session.Key,
	configID string,
	kind sessionConfigOptionKind,
	selectValue string,
	booleanValue bool,
) error {
	if a == nil {
		return ErrSessionNotActive
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("acpagent: invalid session key: %w", err)
	}
	releaseActivity := a.connections.beginActivity()
	defer releaseActivity()

	binding := a.sessions.lookup(key)
	if binding == nil {
		return ErrSessionNotActive
	}
	if err := binding.acquirePrompt(ctx); err != nil {
		return err
	}
	defer binding.releasePrompt()

	nextBinding, release, err := a.ensureConfigSessionBinding(ctx, binding)
	if err != nil {
		return err
	}
	if nextBinding == nil || release == nil {
		return ErrSessionNotActive
	}
	binding = nextBinding
	defer release()

	config := binding.sessionConfigSnapshot()
	option, ok := config.find(configID)
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionConfigOptionNotFound, configID)
	}
	if option.kind != kind {
		return fmt.Errorf("%w: %q", ErrSessionConfigOptionTypeMismatch, configID)
	}
	if kind == sessionConfigOptionKindSelect && !option.allowsSelectValue(selectValue) {
		return fmt.Errorf("%w: %q=%q", ErrSessionConfigValueNotAllowed, configID, selectValue)
	}

	request := acpsdk.SetSessionConfigOptionRequest{}
	if kind == sessionConfigOptionKindSelect {
		request.ValueId = &acpsdk.SetSessionConfigOptionValueId{
			SessionId: binding.id,
			ConfigId:  acpsdk.SessionConfigId(configID),
			Value:     acpsdk.SessionConfigValueId(selectValue),
		}
	} else {
		request.Boolean = &acpsdk.SetSessionConfigOptionBoolean{
			SessionId: binding.id,
			ConfigId:  acpsdk.SessionConfigId(configID),
			Type:      "boolean",
			Value:     booleanValue,
		}
	}
	response, err := binding.process.connection.SetSessionConfigOption(ctx, request)
	if err != nil {
		return fmt.Errorf("acpagent: set session config option: %w", err)
	}
	binding.replaceSessionConfig(response.ConfigOptions)
	infoContext(
		ctx,
		"acpagent: set session config option",
		append(
			acpSessionLogFields(binding),
			"agent", a.config.Name,
			"config_id", configID,
			"config_type", option.kind.String(),
		)...,
	)
	return nil
}

func (a *ACPAgent) ensureConfigSessionBinding(
	ctx context.Context,
	binding *acpSession,
) (*acpSession, func(), error) {
	process, err := a.connections.connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	if binding.process == process && binding.generation == process.generation {
		return binding, func() {}, nil
	}

	supported, config, err := a.restoreACPSession(ctx, process, binding.id)
	if err != nil {
		if isUnknownSessionError(err) {
			a.sessions.remove(binding.key, binding)
			return nil, nil, errors.Join(ErrSessionNotActive, err)
		}
		return nil, nil, err
	}
	if !supported {
		return nil, nil, ErrSessionNotActive
	}

	rebound := a.newSessionBinding(
		binding.key,
		binding.id,
		process,
		binding.createdAt,
		nil,
		config,
	)
	if err := rebound.acquirePrompt(ctx); err != nil {
		return nil, nil, err
	}
	a.sessions.put(rebound)
	return rebound, func() {
		rebound.releasePrompt()
	}, nil
}
