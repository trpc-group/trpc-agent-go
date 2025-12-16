//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package modelspec provides helpers for parsing and resolving the common
// model_spec config block used by builtin.llm/builtin.llmagent.
package modelspec

import (
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

type Spec struct {
	Provider    string
	ModelName   string
	APIKey      string
	BaseURL     string
	Headers     map[string]string
	ExtraFields map[string]any
}

// Parse validates model_spec and returns a normalized Spec.
// It is strict: passing nil or a non-object will return an error.
func Parse(modelSpec any) (Spec, error) {
	specMap, ok := modelSpec.(map[string]any)
	if !ok || specMap == nil {
		return Spec{}, fmt.Errorf("model_spec must be an object")
	}

	providerRaw, ok := specMap["provider"]
	if !ok || providerRaw == nil {
		return Spec{}, fmt.Errorf("model_spec.provider is required")
	}
	provider, ok := providerRaw.(string)
	if !ok {
		return Spec{}, fmt.Errorf("model_spec.provider must be a string")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return Spec{}, fmt.Errorf("model_spec.provider is required")
	}

	modelNameRaw, ok := specMap["model_name"]
	if !ok || modelNameRaw == nil {
		return Spec{}, fmt.Errorf("model_spec.model_name is required")
	}
	modelName, ok := modelNameRaw.(string)
	if !ok {
		return Spec{}, fmt.Errorf("model_spec.model_name must be a string")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return Spec{}, fmt.Errorf("model_spec.model_name is required")
	}

	apiKeyRaw, ok := specMap["api_key"]
	if !ok || apiKeyRaw == nil {
		return Spec{}, fmt.Errorf("model_spec.api_key is required")
	}
	apiKey, ok := apiKeyRaw.(string)
	if !ok {
		return Spec{}, fmt.Errorf("model_spec.api_key must be a string")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Spec{}, fmt.Errorf("model_spec.api_key is required")
	}

	var baseURL string
	if baseURLRaw, ok := specMap["base_url"]; ok && baseURLRaw != nil {
		baseURLStr, ok := baseURLRaw.(string)
		if !ok {
			return Spec{}, fmt.Errorf("model_spec.base_url must be a string")
		}
		baseURL = strings.TrimSpace(baseURLStr)
	}

	var headers map[string]string
	if headersRaw, ok := specMap["headers"]; ok && headersRaw != nil {
		switch h := headersRaw.(type) {
		case map[string]any:
			headers = make(map[string]string)
			for k, v := range h {
				vStr, ok := v.(string)
				if !ok {
					return Spec{}, fmt.Errorf("model_spec.headers[%q] must be a string", k)
				}
				vStr = strings.TrimSpace(vStr)
				if vStr != "" {
					headers[k] = vStr
				}
			}
		case map[string]string:
			if len(h) > 0 {
				headers = make(map[string]string, len(h))
				for k, v := range h {
					v = strings.TrimSpace(v)
					if v != "" {
						headers[k] = v
					}
				}
			}
		default:
			return Spec{}, fmt.Errorf("model_spec.headers must be an object")
		}
	}

	var extraFields map[string]any
	if extraFieldsRaw, ok := specMap["extra_fields"]; ok && extraFieldsRaw != nil {
		ef, ok := extraFieldsRaw.(map[string]any)
		if ok && len(ef) > 0 {
			extraFields = ef
		}
	}

	return Spec{
		Provider:    provider,
		ModelName:   modelName,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Headers:     headers,
		ExtraFields: extraFields,
	}, nil
}

// ResolveEnv resolves "env:VAR" placeholders in sensitive string fields.
// It is intended for local debugging only; production callers should pass
// fully-resolved secrets instead.
func ResolveEnv(spec Spec, allowEnvSecrets bool) (Spec, error) {
	resolveEnvString := func(value string, fieldPath string) (string, error) {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "env:") {
			return value, nil
		}
		if !allowEnvSecrets {
			return "", fmt.Errorf("%s uses an env placeholder but env secrets are disabled (enable compiler.WithAllowEnvSecrets(true) for local debugging)", fieldPath)
		}
		varName := strings.TrimSpace(strings.TrimPrefix(value, "env:"))
		if varName == "" {
			return "", fmt.Errorf("%s env placeholder is invalid (expected env:VAR)", fieldPath)
		}
		envVal, ok := os.LookupEnv(varName)
		if !ok || strings.TrimSpace(envVal) == "" {
			return "", fmt.Errorf("environment variable %q is not set (required by %s)", varName, fieldPath)
		}
		return envVal, nil
	}

	apiKey, err := resolveEnvString(spec.APIKey, "model_spec.api_key")
	if err != nil {
		return Spec{}, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Spec{}, fmt.Errorf("model_spec.api_key is required")
	}
	spec.APIKey = apiKey

	if spec.BaseURL != "" {
		baseURL, err := resolveEnvString(spec.BaseURL, "model_spec.base_url")
		if err != nil {
			return Spec{}, err
		}
		spec.BaseURL = strings.TrimSpace(baseURL)
	}

	if len(spec.Headers) > 0 {
		headers := make(map[string]string, len(spec.Headers))
		for k, v := range spec.Headers {
			vResolved, err := resolveEnvString(v, fmt.Sprintf("model_spec.headers[%q]", k))
			if err != nil {
				return Spec{}, err
			}
			vResolved = strings.TrimSpace(vResolved)
			if vResolved != "" {
				headers[k] = vResolved
			}
		}
		if len(headers) > 0 {
			spec.Headers = headers
		} else {
			spec.Headers = nil
		}
	}

	return spec, nil
}

// NewModel constructs a concrete model instance from a resolved Spec.
func NewModel(spec Spec) (model.Model, string, error) {
	switch strings.ToLower(spec.Provider) {
	case "openai":
		var opts []openai.Option
		opts = append(opts, openai.WithAPIKey(spec.APIKey))
		if spec.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(spec.BaseURL))
		}
		if len(spec.Headers) > 0 {
			opts = append(opts, openai.WithHeaders(spec.Headers))
		}
		if len(spec.ExtraFields) > 0 {
			opts = append(opts, openai.WithExtraFields(spec.ExtraFields))
		}
		return openai.New(spec.ModelName, opts...), spec.ModelName, nil
	default:
		return nil, "", fmt.Errorf("unsupported model_spec.provider %q (only \"openai\" is supported in this version)", spec.Provider)
	}
}
