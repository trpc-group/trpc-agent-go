//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

const legacyDefaultModelAlias = "default"

type resolvedModelCatalog struct {
	defaultName string
	models      map[string]model.Model
	metadata    map[string]ModelMetadata
	explicit    bool
}

func validateModelCatalogAgentType(
	agentType string,
	opts runOptions,
	runtimeOpts runtimeOptions,
) error {
	if agentType == agentTypeLLM {
		return nil
	}
	if len(opts.ModelEntries) == 0 && runtimeOpts.modelCatalog == nil {
		return nil
	}
	return fmt.Errorf(
		"request-scoped model catalogs require agent-type %q",
		agentTypeLLM,
	)
}

func resolveModelCatalog(
	opts runOptions,
	runtimeOpts runtimeOptions,
) (resolvedModelCatalog, error) {
	if runtimeOpts.modelCatalog != nil && len(opts.ModelEntries) > 0 {
		return resolvedModelCatalog{}, fmt.Errorf(
			"WithModelCatalog cannot be combined with model.models",
		)
	}
	if runtimeOpts.modelCatalog != nil {
		return resolveInjectedModelCatalog(opts, *runtimeOpts.modelCatalog)
	}
	if len(opts.ModelEntries) > 0 {
		return resolveConfiguredModelCatalog(opts)
	}

	mdl, err := modelFromOptions(opts)
	if err != nil {
		return resolvedModelCatalog{}, err
	}
	alias := strings.TrimSpace(mdl.Info().Name)
	if alias == "" {
		alias = strings.TrimSpace(opts.OpenAIModel)
	}
	if alias == "" {
		alias = legacyDefaultModelAlias
	}
	return resolvedModelCatalog{
		defaultName: alias,
		models: map[string]model.Model{
			alias: newModelCallBudgetModel(mdl),
		},
		metadata: map[string]ModelMetadata{
			alias: modelMetadataFromRunOptions(opts),
		},
	}, nil
}

func resolveInjectedModelCatalog(
	opts runOptions,
	catalog ModelCatalog,
) (resolvedModelCatalog, error) {
	defaultName := strings.TrimSpace(catalog.Default)
	if defaultName == "" {
		return resolvedModelCatalog{}, fmt.Errorf(
			"model catalog default is required",
		)
	}
	models, err := normalizeModelMap(catalog.Models)
	if err != nil {
		return resolvedModelCatalog{}, err
	}
	if _, ok := models[defaultName]; !ok {
		return resolvedModelCatalog{}, fmt.Errorf(
			"model catalog default %q is not configured",
			defaultName,
		)
	}
	for name, mdl := range models {
		models[name] = newModelCallBudgetModel(mdl)
	}
	metadata, err := normalizeModelMetadata(
		catalog.Metadata,
		models,
		modelMetadataFromRunOptions(opts),
	)
	if err != nil {
		return resolvedModelCatalog{}, err
	}
	return resolvedModelCatalog{
		defaultName: defaultName,
		models:      models,
		metadata:    metadata,
		explicit:    true,
	}, nil
}

func resolveConfiguredModelCatalog(
	opts runOptions,
) (resolvedModelCatalog, error) {
	defaultName := strings.TrimSpace(opts.ModelDefault)
	if defaultName == "" {
		return resolvedModelCatalog{}, fmt.Errorf(
			"model.default is required when model.models is configured",
		)
	}
	names := make([]string, 0, len(opts.ModelEntries))
	for name := range opts.ModelEntries {
		names = append(names, name)
	}
	sort.Strings(names)

	models := make(map[string]model.Model, len(names))
	metadata := make(map[string]ModelMetadata, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return resolvedModelCatalog{}, fmt.Errorf(
				"model.models contains an empty alias",
			)
		}
		if _, exists := models[name]; exists {
			return resolvedModelCatalog{}, fmt.Errorf(
				"model.models contains duplicate alias %q after trimming",
				name,
			)
		}
		entryOpts, err := modelRunOptionsForEntry(
			opts,
			name,
			opts.ModelEntries[rawName],
		)
		if err != nil {
			return resolvedModelCatalog{}, err
		}
		mdl, err := modelFromOptions(entryOpts)
		if err != nil {
			return resolvedModelCatalog{}, fmt.Errorf(
				"model.models.%s: %w",
				name,
				err,
			)
		}
		models[name] = newModelCallBudgetModel(mdl)
		metadata[name] = modelMetadataFromRunOptions(entryOpts)
	}
	if _, ok := models[defaultName]; !ok {
		return resolvedModelCatalog{}, fmt.Errorf(
			"model.default %q is not present in model.models",
			defaultName,
		)
	}
	return resolvedModelCatalog{
		defaultName: defaultName,
		models:      models,
		metadata:    metadata,
		explicit:    true,
	}, nil
}

func normalizeModelMetadata(
	src map[string]ModelMetadata,
	models map[string]model.Model,
	fallback ModelMetadata,
) (map[string]ModelMetadata, error) {
	normalizedSrc := make(map[string]ModelMetadata, len(src))
	for rawName, metadata := range src {
		name := strings.TrimSpace(rawName)
		if _, ok := models[name]; !ok {
			return nil, fmt.Errorf(
				"model catalog metadata entry %q has no matching model",
				name,
			)
		}
		if _, exists := normalizedSrc[name]; exists {
			return nil, fmt.Errorf(
				"model catalog metadata contains duplicate alias %q after trimming",
				name,
			)
		}
		normalizedSrc[name] = metadata
	}
	out := make(map[string]ModelMetadata, len(models))
	for name := range models {
		metadata := fallback
		if configured, ok := normalizedSrc[name]; ok {
			if value := strings.TrimSpace(configured.Mode); value != "" {
				metadata.Mode = value
			}
			if value := strings.TrimSpace(configured.BaseURL); value != "" ||
				configured.BaseURLSet {
				metadata.BaseURL = value
			}
			if value := strings.TrimSpace(configured.OpenAIVariant); value != "" {
				metadata.OpenAIVariant = value
			}
		}
		metadata = normalizeModelMetadataEntry(metadata)
		if metadata.Mode == modeOpenAI {
			if _, err := parseOpenAIVariant(
				metadata.OpenAIVariant,
				metadata.BaseURL,
			); err != nil {
				return nil, fmt.Errorf(
					"model catalog metadata entry %q: %w",
					name,
					err,
				)
			}
		}
		out[name] = metadata
	}
	return out, nil
}

func normalizeModelMetadataEntry(metadata ModelMetadata) ModelMetadata {
	metadata.Mode = strings.ToLower(strings.TrimSpace(metadata.Mode))
	if metadata.Mode == "" {
		metadata.Mode = modeOpenAI
	}
	metadata.BaseURL = strings.TrimSpace(metadata.BaseURL)
	metadata.OpenAIVariant = strings.TrimSpace(metadata.OpenAIVariant)
	if metadata.OpenAIVariant == "" {
		metadata.OpenAIVariant = defaultOpenAIVariant
	}
	return metadata
}

func modelMetadataFromRunOptions(opts runOptions) ModelMetadata {
	return normalizeModelMetadataEntry(ModelMetadata{
		Mode:          opts.ModelMode,
		BaseURL:       resolvedOpenAIBaseURL(opts),
		OpenAIVariant: opts.OpenAIVariant,
	})
}

func normalizeModelMap(
	src map[string]model.Model,
) (map[string]model.Model, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("model catalog models are required")
	}
	out := make(map[string]model.Model, len(src))
	for rawName, mdl := range src {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("model catalog contains an empty alias")
		}
		if mdl == nil {
			return nil, fmt.Errorf("model catalog entry %q is nil", name)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf(
				"model catalog contains duplicate alias %q after trimming",
				name,
			)
		}
		out[name] = mdl
	}
	return out, nil
}

func modelRunOptionsForEntry(
	parent runOptions,
	alias string,
	cfg modelEntryConfig,
) (runOptions, error) {
	opts := runOptions{
		ModelMode:              modeOpenAI,
		OpenAIModel:            defaultOpenAIModelName(),
		OpenAIVariant:          defaultOpenAIVariant,
		OpenAIMaxRetries:       -1,
		OpenAIUseVariantAPIKey: true,
		DebugRecorderEnabled:   parent.DebugRecorderEnabled,
	}
	if cfg.Mode != nil {
		opts.ModelMode = strings.TrimSpace(*cfg.Mode)
	}
	if cfg.Name != nil {
		opts.OpenAIModel = strings.TrimSpace(*cfg.Name)
	}
	if cfg.BaseURL != nil {
		opts.OpenAIBaseURL = strings.TrimSpace(*cfg.BaseURL)
	}
	if cfg.OpenAIVariant != nil {
		opts.OpenAIVariant = strings.TrimSpace(*cfg.OpenAIVariant)
	}
	if cfg.TextOnlyContent != nil {
		opts.OpenAITextOnlyMessageContent = *cfg.TextOnlyContent
	}
	if cfg.Timeout != nil {
		timeout, err := parseDuration(*cfg.Timeout)
		if err != nil {
			return runOptions{}, fmt.Errorf(
				"model.models.%s.timeout: %w",
				alias,
				err,
			)
		}
		if timeout < 0 {
			return runOptions{}, fmt.Errorf(
				"model.models.%s.timeout must be >= 0",
				alias,
			)
		}
		opts.OpenAITimeout = timeout
	}
	if cfg.MaxRetries != nil {
		if *cfg.MaxRetries < 0 {
			return runOptions{}, fmt.Errorf(
				"model.models.%s.max_retries must be >= 0",
				alias,
			)
		}
		opts.OpenAIMaxRetries = *cfg.MaxRetries
		opts.OpenAIMaxRetriesSet = true
	}
	opts.OpenAIHeaders = cleanHeaderMap(cfg.Headers)
	if cfg.Config != nil {
		opts.ModelConfig = cfg.Config.Node
	}
	return opts, nil
}

func (c resolvedModelCatalog) defaultModel() model.Model {
	return c.models[c.defaultName]
}

func (c resolvedModelCatalog) modelNames() []string {
	names := make([]string, 0, len(c.models))
	for name := range c.models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c resolvedModelCatalog) selectedModelName(
	ctx context.Context,
	requestModelName string,
) string {
	if name := strings.TrimSpace(requestModelName); name != "" {
		return name
	}
	if profile, ok := runtimeprofile.ProfileFromContext(ctx); ok {
		if name := strings.TrimSpace(profile.ModelName); name != "" {
			return name
		}
	}
	return c.defaultName
}

func (c resolvedModelCatalog) runOptionsForModel(
	base runOptions,
	name string,
) runOptions {
	metadata, ok := c.metadata[strings.TrimSpace(name)]
	if !ok {
		return base
	}
	base.ModelMode = metadata.Mode
	base.OpenAIBaseURL = metadata.BaseURL
	base.OpenAIVariant = metadata.OpenAIVariant
	return base
}

func (c resolvedModelCatalog) defaultAlias() string {
	return strings.TrimSpace(c.defaultName)
}

func (c resolvedModelCatalog) adminRunOptions(base runOptions) runOptions {
	if !c.explicit {
		return base
	}
	base.OpenAIModel = c.defaultAlias()
	if metadata, ok := c.metadata[c.defaultName]; ok {
		base.ModelMode = metadata.Mode
		base.OpenAIVariant = metadata.OpenAIVariant
		base.OpenAIBaseURL = metadata.BaseURL
	}
	return base
}

func (c resolvedModelCatalog) identityParts() []string {
	names := c.modelNames()
	parts := make([]string, 0, 1+(len(names)*5))
	parts = append(parts, "default="+c.defaultAlias())
	for _, name := range names {
		metadata := c.metadata[name]
		modelName := ""
		if mdl := c.models[name]; mdl != nil {
			modelName = strings.TrimSpace(mdl.Info().Name)
		}
		parts = append(
			parts,
			"alias="+name,
			"model="+modelName,
			"mode="+metadata.Mode,
			"variant="+metadata.OpenAIVariant,
			"base_url="+metadata.BaseURL,
		)
	}
	return parts
}

func appendModelCatalogGatewayOptions(
	opts []gateway.Option,
	catalog resolvedModelCatalog,
) []gateway.Option {
	if !catalog.explicit || len(catalog.models) == 0 {
		return opts
	}
	opts = append(
		opts,
		gateway.WithSelectableModels(catalog.modelNames()...),
		gateway.WithRunOptionResolver(
			func(
				ctx context.Context,
				input gateway.RunOptionInput,
			) (context.Context, []agent.RunOption, error) {
				requestModelName := strings.TrimSpace(input.ModelName)
				if requestModelName != "" {
					return ctx, []agent.RunOption{
						agent.WithModelName(requestModelName),
					}, nil
				}
				if err := validateResolvedRuntimeProfileModel(
					ctx,
					catalog,
				); err != nil {
					return ctx, nil, err
				}
				return ctx, nil, nil
			},
		),
	)
	return opts
}

func appendModelCatalogCallBudgetGatewayOption(
	opts []gateway.Option,
	runOpts runOptions,
	catalog resolvedModelCatalog,
	limit int,
	finalizeOnLast bool,
	deadlineWindow time.Duration,
) []gateway.Option {
	if limit <= 0 && deadlineWindow <= 0 {
		return opts
	}
	if !catalog.explicit || len(catalog.models) == 0 {
		return appendModelCallBudgetGatewayOption(
			opts,
			limit,
			finalizeOnLast,
			deadlineWindow,
			modelCallBudgetFinalRequestFromOptions(runOpts),
		)
	}
	return append(opts, gateway.WithRunOptionResolver(func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		name := catalog.selectedModelName(ctx, input.ModelName)
		selected := catalog.runOptionsForModel(runOpts, name)
		finalRequest := modelCallBudgetFinalRequestFromOptions(selected)
		return ctx, modelCallBudgetRunOptions(
			limit,
			finalizeOnLast,
			deadlineWindow,
			finalRequest,
		), nil
	}))
}

func validateResolvedRuntimeProfileModel(
	ctx context.Context,
	catalog resolvedModelCatalog,
) error {
	if !catalog.explicit {
		return nil
	}
	profile, ok := runtimeprofile.ProfileFromContext(ctx)
	if !ok {
		return nil
	}
	name := strings.TrimSpace(profile.ModelName)
	if name == "" {
		return nil
	}
	if _, ok := catalog.models[name]; ok {
		return nil
	}
	return fmt.Errorf(
		"runtime profile model %q is not configured",
		name,
	)
}

func validateRuntimeProfileModels(
	agentType string,
	cfg *runtimeprofile.Config,
	catalog resolvedModelCatalog,
) error {
	if agentType != agentTypeLLM || cfg == nil || !catalog.explicit {
		return nil
	}
	for profileID, profile := range cfg.Profiles {
		name := strings.TrimSpace(profile.ModelName)
		if name == "" {
			continue
		}
		if _, ok := catalog.models[name]; !ok {
			return fmt.Errorf(
				"runtime_profiles.profiles.%s.model_name %q is not configured",
				profileID,
				name,
			)
		}
	}
	return nil
}
