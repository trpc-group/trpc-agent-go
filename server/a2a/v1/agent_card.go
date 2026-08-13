//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const agentCardContentType = "application/json; charset=utf-8"

// AgentCardOption configures optional parameters for NewAgentCard.
type AgentCardOption func(*agentCardOptions)

type agentCardOptions struct {
	tools []tool.Tool
}

// WithCardTools sets the tools to be advertised as skills in the AgentCard.
// Each tool's Declaration (Name, Description) will be converted to an AgentSkill
// with the "tool" tag. A default agent-level skill is always prepended.
//
// If no tools are provided, only the default agent-level skill is created.
func WithCardTools(tools ...tool.Tool) AgentCardOption {
	return func(o *agentCardOptions) {
		o.tools = tools
	}
}

// NewAgentCard builds a basic AgentCard from explicit metadata.
// Optional AgentCardOption values can be provided to customize the card,
// e.g. WithCardTools to advertise agent tools as skills.
func NewAgentCard(
	name string,
	description string,
	version string,
	host string,
	streaming bool,
	opts ...AgentCardOption,
) (a2aprotocolserver.AgentCard, error) {
	if name == "" {
		return a2aprotocolserver.AgentCard{}, errors.New("agent name is required")
	}
	if version == "" {
		return a2aprotocolserver.AgentCard{}, errors.New("agent version is required")
	}
	if host == "" {
		return a2aprotocolserver.AgentCard{}, errors.New("host is required")
	}

	o := &agentCardOptions{}
	for _, opt := range opts {
		opt(o)
	}

	endpoint := normalizeJSONRPCEndpoint(host)
	skills := buildSkillsFromCardTools(o.tools, name, description)

	return a2aprotocolserver.AgentCard{
		Name:        name,
		Description: description,
		Version:     version,
		URL:         endpoint,
		SupportedInterfaces: []a2aprotocolserver.AgentInterface{
			{
				URL:             endpoint,
				ProtocolBinding: "JSONRPC",
				ProtocolVersion: protocol.ProtocolVersionV1,
			},
		},
		Capabilities: a2aprotocolserver.AgentCapabilities{
			Streaming: &streaming,
			Extensions: []a2aprotocolserver.AgentExtension{
				{
					URI: ia2a.ExtensionTRPCA2AVersion,
					Params: map[string]any{
						"version": ia2a.InteractionVersion,
					},
				},
			},
		},
		Skills:             skills,
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}, nil
}

// normalizeJSONRPCEndpoint returns the exact endpoint mounted by
// trpc-a2a-go's JSON-RPC server. A non-root base path is served with a trailing
// slash, so the Agent Card must advertise that slash to prevent POST requests
// from being redirected as GET requests.
func normalizeJSONRPCEndpoint(host string) string {
	endpoint := ia2a.NormalizeURL(host)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	if parsed.RawPath != "" && !strings.HasSuffix(parsed.RawPath, "/") {
		parsed.RawPath += "/"
	}
	return parsed.String()
}

// buildSkillsFromCardTools converts tool declarations to AgentSkills.
// It always prepends a default agent-level skill, then appends one skill
// per tool that has a non-nil Declaration.
func buildSkillsFromCardTools(
	tools []tool.Tool,
	agentName string,
	agentDesc string,
) []a2aprotocolserver.AgentSkill {
	descCopy := agentDesc
	defaultSkill := a2aprotocolserver.AgentSkill{
		ID:          "agent",
		Name:        agentName,
		Description: &descCopy,
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		Tags:        []string{"default"},
	}

	if len(tools) == 0 {
		return []a2aprotocolserver.AgentSkill{defaultSkill}
	}

	skills := make([]a2aprotocolserver.AgentSkill, 0, len(tools)+1)
	skills = append(skills, defaultSkill)
	usedSkillIDs := map[string]struct{}{defaultSkill.ID: {}}

	for _, t := range tools {
		if t == nil {
			continue
		}
		decl := t.Declaration()
		if decl == nil {
			continue
		}
		baseSkillID := "tool-" + decl.Name
		skillID := baseSkillID
		for suffix := 2; ; suffix++ {
			if _, exists := usedSkillIDs[skillID]; !exists {
				break
			}
			skillID = fmt.Sprintf("%s-%d", baseSkillID, suffix)
		}
		usedSkillIDs[skillID] = struct{}{}
		toolDesc := decl.Description
		skills = append(skills, a2aprotocolserver.AgentSkill{
			ID:          skillID,
			Name:        decl.Name,
			Description: &toolDesc,
			InputModes:  []string{"text"},
			OutputModes: []string{"text"},
			Tags:        []string{"tool"},
		})
	}

	return skills
}

// NewAgentCardHandler returns a handler that serves AgentCard snapshots
// provided by getter. The getter can read from any caller-managed state.
func NewAgentCardHandler(getter func() a2aprotocolserver.AgentCard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAgentCard(w, r, getter)
	})
}

func writeAgentCard(
	w http.ResponseWriter,
	r *http.Request,
	getter func() a2aprotocolserver.AgentCard,
) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodGet)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if getter == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	payload, err := json.Marshal(getter())
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", agentCardContentType)
	_, _ = w.Write(append(payload, '\n'))
}
