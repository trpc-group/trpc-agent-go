//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

const promptVariantMarkerPrefix = "[[trpc-promptiter-candidate:"

// OptimizeRequest contains failure feedback for one PromptIter proposal.
type OptimizeRequest struct {
	Round          int
	BaselinePrompt string
	Train          *EvaluationSummary
}

// Optimizer proposes a PromptIter profile without deciding whether it is safe to publish.
type Optimizer interface {
	Propose(ctx context.Context, request OptimizeRequest) (*Candidate, error)
}

// DeterministicPromptIter is an offline PromptIter adapter. It emits the same
// Profile and PatchSet domain contracts as the production engine while using a
// scripted proposal list so the full loop works without an API key.
type DeterministicPromptIter struct {
	config Config
}

// NewDeterministicPromptIter creates a reproducible proposal generator.
func NewDeterministicPromptIter(config Config) (*DeterministicPromptIter, error) {
	if err := validateConfig(&config); err != nil {
		return nil, err
	}
	return &DeterministicPromptIter{config: cloneConfig(config)}, nil
}

// Propose returns a one-surface instruction patch for the requested round.
func (o *DeterministicPromptIter) Propose(
	ctx context.Context,
	request OptimizeRequest,
) (*Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Round <= 0 || request.Round > o.config.MaxRounds {
		return nil, fmt.Errorf("round %d is outside configured range", request.Round)
	}
	if strings.TrimSpace(request.BaselinePrompt) == "" {
		return nil, errors.New("baseline prompt is empty")
	}
	if request.Train == nil {
		return nil, errors.New("training evaluation is nil")
	}
	proposal := o.config.Candidates[request.Round-1]
	matched := matchedFailureCategories(request.Train, proposal.AddressCategories)
	prompt := buildCandidatePrompt(
		request.BaselinePrompt,
		proposal,
		matched,
		o.config.Seed,
	)
	surfaceType := astructure.SurfaceType(o.config.Surface.Type)
	surfaceID := astructure.SurfaceID(o.config.Surface.NodeID, surfaceType)
	reason := proposal.Reason
	if len(proposal.AddressCategories) > 0 {
		reason = fmt.Sprintf("%s; matched failure categories: %v", reason, matched)
	}
	patch := promptiter.SurfacePatch{
		SurfaceID: surfaceID,
		Value: astructure.SurfaceValue{
			Text: &prompt,
		},
		Reason: reason,
	}
	profile := &promptiter.Profile{
		StructureID: o.config.Surface.StructureID,
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: surfaceID,
				Value:     patch.Value,
			},
		},
	}
	return &Candidate{
		ID:         proposal.ID,
		Round:      request.Round,
		Prompt:     prompt,
		PromptHash: HashText(prompt),
		SurfaceID:  surfaceID,
		Reason:     reason,
		PatchSet: &promptiter.PatchSet{
			Patches: []promptiter.SurfacePatch{patch},
		},
		Profile: profile,
	}, nil
}

func buildCandidatePrompt(
	baseline string,
	proposal CandidateConfig,
	matched []FailureCategory,
	seed int64,
) string {
	sections := []string{
		strings.TrimSpace(baseline),
		strings.TrimSpace(proposal.AppendPrompt),
	}
	if rules := failureDerivedRules(matched); rules != "" {
		sections = append(sections, "PromptIter 根据训练失败归因生成的通用约束：\n"+rules)
	}
	sections = append(sections, fmt.Sprintf("%s%s;seed:%d]]", promptVariantMarkerPrefix, proposal.ID, seed))
	return strings.Join(sections, "\n\n")
}

func failureDerivedRules(categories []FailureCategory) string {
	rules := map[FailureCategory]string{
		FailureFinalResponseMismatch:          "- 最终回复必须逐项覆盖参考事实与用户意图。",
		FailureToolCallError:                  "- 先核对工具用途，只调用完成当前任务所需的工具。",
		FailureToolParameterError:             "- 工具参数必须从当前请求提取，禁止复用其他样本参数。",
		FailureRouteError:                     "- 路由前核对任务域，并选择对应 specialist。",
		FailureFormatError:                    "- 严格遵循要求的结构化输出格式并在返回前校验。",
		FailureKnowledgeRetrievalInsufficient: "- 政策与知识问题必须先召回依据，再基于证据作答。",
	}
	lines := make([]string, 0, len(categories))
	for _, category := range categories {
		if rule, ok := rules[category]; ok {
			lines = append(lines, rule)
		}
	}
	return strings.Join(lines, "\n")
}

func promptVariantID(prompt string) (string, bool) {
	id, _, ok := promptVariantMetadata(prompt)
	return id, ok
}

func promptVariantMetadata(prompt string) (string, int64, bool) {
	start := strings.LastIndex(prompt, promptVariantMarkerPrefix)
	if start < 0 {
		return "", 0, false
	}
	remainder := prompt[start+len(promptVariantMarkerPrefix):]
	closing := strings.Index(remainder, "]]")
	if closing <= 0 || strings.TrimSpace(remainder[closing+2:]) != "" {
		return "", 0, false
	}
	payload := remainder[:closing]
	separator := strings.LastIndex(payload, ";seed:")
	if separator <= 0 {
		return "", 0, false
	}
	seed, err := strconv.ParseInt(payload[separator+len(";seed:"):], 10, 64)
	if err != nil {
		return "", 0, false
	}
	id := payload[:separator]
	if !validCandidateID(id) {
		return "", 0, false
	}
	return id, seed, true
}

func semanticPromptContent(prompt string) string {
	if _, ok := promptVariantID(prompt); ok {
		start := strings.LastIndex(prompt, promptVariantMarkerPrefix)
		return strings.TrimSpace(prompt[:start])
	}
	return strings.TrimSpace(prompt)
}

// HashText returns the lowercase SHA-256 digest used in audit records.
func HashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func surfaceIDFromConfig(config SurfaceConfig) string {
	return astructure.SurfaceID(config.NodeID, astructure.SurfaceType(config.Type))
}

func matchedFailureCategories(
	train *EvaluationSummary,
	targets []FailureCategory,
) []FailureCategory {
	targetSet := make(map[FailureCategory]struct{}, len(targets))
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	matchedSet := make(map[FailureCategory]struct{})
	for _, evalCase := range train.Cases {
		for _, attribution := range evalCase.FailureAttributions {
			if _, ok := targetSet[attribution.Category]; ok {
				matchedSet[attribution.Category] = struct{}{}
			}
		}
	}
	matched := make([]FailureCategory, 0, len(matchedSet))
	for _, target := range targets {
		if _, ok := matchedSet[target]; ok {
			matched = append(matched, target)
		}
	}
	return matched
}
