//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type recordingReviewModel struct {
	request   *model.Request
	responses []*model.Response
	err       error
}

func (m *recordingReviewModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.request = req
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan *model.Response, len(m.responses))
	for _, resp := range m.responses {
		ch <- resp
	}
	close(ch)
	return ch, nil
}

func (m *recordingReviewModel) Info() model.Info { return model.Info{Name: "recording-review-model"} }

type blockingReviewModel struct{}

func (m blockingReviewModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	return make(chan *model.Response), nil
}

func (m blockingReviewModel) Info() model.Info { return model.Info{Name: "blocking-review-model"} }

type blockingGenerateReviewModel struct {
	started chan struct{}
	release chan struct{}
}

func (m blockingGenerateReviewModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	close(m.started)
	<-m.release
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m blockingGenerateReviewModel) Info() model.Info {
	return model.Info{Name: "blocking-generate-review-model"}
}

func TestLLMReviewer_Review_StripsCodeFenceAndNormalizes(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{
				Message: model.Message{Content: "```json\n{\n  \"skills\": [{\n    \"name\": \"  Release Checklist  \",\n    \"description\": \"  Steps to release  \",\n    \"when_to_use\": \"  Before shipping  \",\n    \"steps\": [\" draft notes \", \" publish \", \"  \"],\n    \"pitfalls\": [\" forget tests \", \"  \"]\n  }]\n}\n```"},
			}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	decision, err := reviewer.Review(context.Background(), &ReviewInput{
		AppName:    "bench-app",
		UserID:     "user-1",
		SessionID:  "sess-1",
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "please make this repeatable"}},
	})
	require.NoError(t, err)
	require.Len(t, decision.Skills, 1)
	assert.Equal(t, "Release Checklist", decision.Skills[0].Name)
	assert.Equal(t, "Steps to release", decision.Skills[0].Description)
	assert.Equal(t, "Before shipping", decision.Skills[0].WhenToUse)
	assert.Equal(t, []string{"draft notes", "publish"}, decision.Skills[0].Steps)
	assert.Equal(t, []string{"forget tests"}, decision.Skills[0].Pitfalls)
}

func TestLLMReviewer_Review_ReturnsWhenContextExpiresDuringResponse(t *testing.T) {
	reviewer := NewLLMReviewer(blockingReviewModel{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := reviewer.Review(ctx, &ReviewInput{
		AppName:    "bench-app",
		UserID:     "user-1",
		SessionID:  "sess-1",
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "please make this repeatable"}},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLLMReviewer_Review_ReturnsWhenContextExpiresDuringGenerate(t *testing.T) {
	mdl := blockingGenerateReviewModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(mdl.release)
	reviewer := NewLLMReviewer(mdl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := reviewer.Review(ctx, &ReviewInput{
		AppName:    "bench-app",
		UserID:     "user-1",
		SessionID:  "sess-1",
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "please make this repeatable"}},
	})
	<-mdl.started
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLLMReviewer_Review_IncludesTranscriptAndToolCalls(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		AppName:   "bench-app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Transcript: []ReviewMessage{
			{
				Role:    model.RoleAssistant,
				Content: "I'll create a reusable release checklist.",
				ToolCalls: []ReviewToolCall{{
					ID:        "call-1",
					Name:      "workspace_exec",
					Arguments: `{"command":"cat > skills/release/SKILL.md <<'EOF'"}`,
				}},
			},
			{
				Role:     model.RoleTool,
				ToolName: "workspace_exec",
				Content:  "wrote skills/release/SKILL.md",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	require.Len(t, reviewModel.request.Messages, 2)
	prompt := reviewModel.request.Messages[1].Content
	assert.Contains(t, prompt, "## Transcript")
	assert.Contains(t, prompt, "workspace_exec")
	assert.Contains(t, prompt, "SKILL.md")
	assert.Contains(t, prompt, "Tool calls:")
}

func TestLLMReviewer_Review_SystemPromptRequiresScopeAccurateSkills(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "extract a reusable skill"}},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	require.Len(t, reviewModel.request.Messages, 2)
	systemPrompt := reviewModel.request.Messages[0].Content
	assert.Contains(t, systemPrompt, "scope-accurate")
	assert.Contains(t, systemPrompt, "name the skill narrowly")
	assert.Contains(t, systemPrompt, "every essential API/tool category")
	assert.Contains(t, systemPrompt, "Do not omit required steps")
}

func TestLLMReviewer_Review_InvalidJSON(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `definitely not valid reviewer json`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse reviewer output")
}

func TestLLMReviewer_Review_ParsesJSONWrappedInProse(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{
				Message: model.Message{Content: "Here is the decision:\n{\"skip_reason\":\"nothing useful\"}\nThanks."},
			}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	decision, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.NoError(t, err)
	require.NotNil(t, decision)
	assert.Equal(t, "nothing useful", decision.SkipReason)
}

func TestLLMReviewer_Review_RepairsMalformedJSON(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{
				Message: model.Message{Content: "```json\n{skip_reason: 'nothing useful', updates: [],}\n```"},
			}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	decision, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.NoError(t, err)
	require.NotNil(t, decision)
	assert.Equal(t, "nothing useful", decision.SkipReason)
}

func TestLLMReviewer_Review_ResponseError(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Error: &model.ResponseError{Message: "provider failed"},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider failed")
}

func TestLLMReviewer_Review_GenerateError(t *testing.T) {
	reviewModel := &recordingReviewModel{
		err: errors.New("dial failed"),
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "teach me"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial failed")
}

func TestLLMReviewer_Review_TruncatesLongToolResults(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel, WithMessageContentMaxChars(200))

	huge := strings.Repeat("HEAD-", 100) + strings.Repeat("MID-", 5000) + strings.Repeat("TAIL-", 100)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		AppName:   "bench-app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Transcript: []ReviewMessage{
			{
				Role:     model.RoleTool,
				ToolName: "weather_get_hourly",
				Content:  huge,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	require.Len(t, reviewModel.request.Messages, 2)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "weather_get_hourly")
	assert.Contains(t, prompt, "HEAD-")
	assert.Contains(t, prompt, "TAIL-")
	assert.Contains(t, prompt, "chars omitted by reviewer transcript truncation")
	assert.Less(t, len(prompt), len(huge)/4,
		"truncated prompt should be much smaller than the raw payload")
}

func TestLLMReviewer_Review_RedactsSecretsInPrompt(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{
			{
				Role:    model.RoleAssistant,
				Content: "use OPENAI_API_KEY=sk-test-REDACT-ME-000 and Authorization: Bearer " + "eyJhbGciOiJIUzI1NiJ9" + ".payload.sig",
				ToolCalls: []ReviewToolCall{{
					Name:      "workspace_exec",
					Arguments: `{"api_key":"sk-test-REDACT-ME-111","token":"tok-FAKE-0000000"}`,
				}},
			},
		},
		Outcome: &Outcome{
			Status: OutcomeFail,
			Notes:  "Authorization: Bearer tok-FAKE-0000000",
		},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.NotContains(t, prompt, "sk-test-REDACT-ME-000")
	assert.NotContains(t, prompt, "sk-test-REDACT-ME-111")
	assert.NotContains(t, prompt, "tok-FAKE-0000000")
	assert.Contains(t, prompt, reviewerRedactedValue)
}

func TestLLMReviewer_Review_DefaultMessageMaxCharsApplied(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	huge := strings.Repeat("X", DefaultReviewerMessageMaxChars*4)
	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleTool, ToolName: "huge_payload", Content: huge}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "chars omitted")
	assert.Less(t, len(prompt), len(huge),
		"default truncation should shrink an oversized transcript")
}

func TestLLMReviewer_Review_ShortMessagesNotTruncated(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel, WithMessageContentMaxChars(500))

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleTool, ToolName: "tiny", Content: "small payload"}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "small payload")
	assert.NotContains(t, prompt, "chars omitted")
}

func TestNormalizeReviewDecision_RejectsInvalidMixedSkipAndSkills(t *testing.T) {
	_, err := normalizeReviewDecision(&ReviewDecision{
		SkipReason: "skip",
		Skills: []*SkillSpec{{
			Name:        "Skill",
			Description: "desc",
			WhenToUse:   "when",
			Steps:       []string{"step"},
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skip_reason cannot coexist")
}

func TestNormalizeReviewDecision_RejectsIncompleteSkill(t *testing.T) {
	_, err := normalizeReviewDecision(&ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Skill",
			Description: "",
			WhenToUse:   "when",
			Steps:       []string{"step"},
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill description is required")
}

func TestLLMReviewer_Review_RendersExistingSkillIndex(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"nothing useful"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
		ExistingSkills: []ExistingSkill{
			{Name: "release-checklist", Description: "Steps to ship"},
			{Name: "bare-name"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reviewModel.request)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "## Existing skills")
	assert.Contains(t, prompt, "- release-checklist: Steps to ship")
	assert.Contains(t, prompt, "- bare-name\n")
}

func TestLLMReviewer_Review_RendersBodyExcerptWhenProvided(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	excerpt := "## Steps\n1. fetch foo\n2. transform foo\n3. save bar"
	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
		ExistingSkills: []ExistingSkill{{
			Name:        "foo-to-bar",
			Description: "Convert foo into bar",
			BodyExcerpt: excerpt,
		}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "- foo-to-bar: Convert foo into bar")
	assert.Contains(t, prompt, "  body excerpt:")
	for _, line := range strings.Split(excerpt, "\n") {
		assert.Contains(t, prompt, "  | "+line)
	}
}

func TestLLMReviewer_Review_DoesNotFabricateBodyWhenCallerOmitsIt(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
		ExistingSkills: []ExistingSkill{{
			Name:        "no-body",
			Description: "description only",
		}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "- no-body: description only")
	assert.NotContains(t, prompt, "body excerpt:",
		"reviewer must not invent a body excerpt when caller did not provide one")
}

func TestLLMReviewer_Review_SystemPromptDescribesUpdatesAndDeletions(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
	})
	require.NoError(t, err)
	system := reviewModel.request.Messages[0].Content
	assert.Contains(t, system, "updates")
	assert.Contains(t, system, "deletions")
	assert.Contains(t, system, "Never list the same skill name in more than one")
	assert.Contains(t, system, `"updates"`)
	assert.Contains(t, system, `"deletions"`)
}

func TestLLMReviewer_Review_SystemPromptHasAntiProliferationRules(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
	})
	require.NoError(t, err)
	system := reviewModel.request.Messages[0].Content

	assert.Contains(t, system, "Anti-proliferation rules",
		"reviewer must be told the dedup rules")
	assert.Contains(t, system, "Quantitative differences",
		"quantitative-only differences should not justify a new skill")
	assert.Contains(t, system, "strict superset of an existing name",
		"name-suffix duplication should be called out")
	assert.Contains(t, system, "skip_reason",
		"reviewer must be reminded that skipping is the right answer when the library already covers the workflow")
}

func TestTruncateBodyExcerpt(t *testing.T) {
	t.Run("short body is returned verbatim", func(t *testing.T) {
		body := "tiny body"
		got := truncateBodyExcerpt(body, 100)
		assert.Equal(t, body, got)
	})

	t.Run("long body is truncated and marked", func(t *testing.T) {
		body := strings.Repeat("x", 500)
		got := truncateBodyExcerpt(body, 80)
		assert.LessOrEqual(t, len(got), 80)
		assert.Contains(t, got, "[truncated]")
	})

	t.Run("non-positive budget keeps body untouched", func(t *testing.T) {
		body := strings.Repeat("y", 100)
		assert.Equal(t, body, truncateBodyExcerpt(body, 0))
		assert.Equal(t, body, truncateBodyExcerpt(body, -1))
	})

	t.Run("trailing whitespace is trimmed before the marker", func(t *testing.T) {
		body := "step 1   \n\nstep 2 with extra " + strings.Repeat("x", 200)
		got := truncateBodyExcerpt(body, 40)
		assert.Contains(t, got, "[truncated]")
		// Marker should follow visible text, not a space run.
		assert.NotContains(t, got, "   \n... [truncated]")
	})
}

func TestLoadExistingSkills_RespectsBodyBudget(t *testing.T) {
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Foo", Description: "foo desc"}},
		bodies:    map[string]string{"Foo": strings.Repeat("F", 1000)},
	}
	t.Run("default budget includes a head excerpt", func(t *testing.T) {
		got := loadExistingSkills(repo, 0)
		require.Len(t, got, 1)
		assert.Equal(t, "Foo", got[0].Name)
		assert.NotEmpty(t, got[0].BodyExcerpt)
		assert.Contains(t, got[0].BodyExcerpt, "[truncated]")
	})
	t.Run("negative budget skips body loading", func(t *testing.T) {
		got := loadExistingSkills(repo, -1)
		require.Len(t, got, 1)
		assert.Empty(t, got[0].BodyExcerpt)
	})
	t.Run("nil repo returns nil", func(t *testing.T) {
		assert.Nil(t, loadExistingSkills(nil, 100))
	})
}

func TestNormalizeReviewDecision_NormalizesUpdatesAndDeletions(t *testing.T) {
	decision, err := normalizeReviewDecision(&ReviewDecision{
		Updates: []*SkillUpdate{{
			Name: "Existing",
			NewSpec: &SkillSpec{
				Description: "  refreshed  ",
				WhenToUse:   "When better",
				Steps:       []string{" step1 ", "  ", "step2"},
			},
		}},
		Deletions: []string{"  Stale  ", "", "Stale"},
	})
	require.NoError(t, err)
	require.Len(t, decision.Updates, 1)
	assert.Equal(t, "Existing", decision.Updates[0].Name)
	assert.Equal(t, "Existing", decision.Updates[0].NewSpec.Name,
		"normalize should force NewSpec.Name to match the update target")
	assert.Equal(t, "refreshed", decision.Updates[0].NewSpec.Description)
	assert.Equal(t, []string{"step1", "step2"}, decision.Updates[0].NewSpec.Steps)
	require.Len(t, decision.Deletions, 1, "duplicate deletions should be collapsed")
	assert.Equal(t, "Stale", decision.Deletions[0])
}

func TestNormalizeReviewDecision_DropsUpdateWithEmptyName(t *testing.T) {
	decision, err := normalizeReviewDecision(&ReviewDecision{
		Updates: []*SkillUpdate{{
			Name: "",
			NewSpec: &SkillSpec{
				Description: "ok",
				WhenToUse:   "ok",
				Steps:       []string{"go"},
			},
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, decision.Updates)
}

func TestNormalizeReviewDecision_NameCollisionPriority(t *testing.T) {
	// Same name shows up in skills, updates, and deletions. Deletions wins,
	// then Updates, then Skills.
	decision, err := normalizeReviewDecision(&ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Conflict",
			Description: "from skills",
			WhenToUse:   "when",
			Steps:       []string{"a"},
		}},
		Updates: []*SkillUpdate{{
			Name: "conflict", // case-insensitive collision with above
			NewSpec: &SkillSpec{
				Description: "from updates",
				WhenToUse:   "when",
				Steps:       []string{"b"},
			},
		}},
		Deletions: []string{"CONFLICT"},
	})
	require.NoError(t, err)
	require.Len(t, decision.Deletions, 1)
	assert.Equal(t, "CONFLICT", decision.Deletions[0])
	assert.Empty(t, decision.Updates, "update must lose to deletion")
	assert.Empty(t, decision.Skills, "skill must lose to deletion")
}

func TestNormalizeReviewDecision_RejectsSkipWithUpdatesOrDeletions(t *testing.T) {
	_, err := normalizeReviewDecision(&ReviewDecision{
		SkipReason: "skip",
		Deletions:  []string{"X"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skip_reason cannot coexist")
}

func TestLLMReviewer_Review_RendersOutcomeBlockWhenSet(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	score := 0.42
	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
		Outcome: &Outcome{
			Status:    OutcomeFail,
			Score:     &score,
			Notes:     "missing economic_snapshot.json",
			Evaluator: "skillcraft",
		},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "## Session outcome")
	assert.Contains(t, prompt, "- status: fail")
	assert.Contains(t, prompt, "- score: 0.4200")
	assert.Contains(t, prompt, "- evaluator: skillcraft")
	assert.Contains(t, prompt, "- notes: missing economic_snapshot.json")
}

func TestLLMReviewer_Review_OmitsOutcomeBlockWhenNil(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.NotContains(t, prompt, "## Session outcome")
}

func TestLLMReviewer_Review_OutcomeWithUnknownStatusRendersUnknownLabel(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
		Outcome:    &Outcome{Notes: "no status reported"},
	})
	require.NoError(t, err)
	prompt := reviewModel.request.Messages[1].Content

	assert.Contains(t, prompt, "## Session outcome")
	assert.Contains(t, prompt, "- status: unknown")
}

// --- Tests for normalizeSkillSpec edge cases ---

func TestNormalizeSkillSpec_NilReturnsNil(t *testing.T) {
	got, err := normalizeSkillSpec(nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestNormalizeSkillSpec_AllEmptyReturnsNil(t *testing.T) {
	got, err := normalizeSkillSpec(&SkillSpec{
		Name:        "  ",
		Description: "  ",
		WhenToUse:   "  ",
		Steps:       []string{"  ", ""},
	})
	require.NoError(t, err)
	assert.Nil(t, got, "all-whitespace spec should normalize to nil")
}

func TestNormalizeSkillSpec_MissingName(t *testing.T) {
	_, err := normalizeSkillSpec(&SkillSpec{
		Name:        "",
		Description: "desc",
		WhenToUse:   "when",
		Steps:       []string{"step"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill name is required")
}

func TestNormalizeSkillSpec_MissingWhenToUse(t *testing.T) {
	_, err := normalizeSkillSpec(&SkillSpec{
		Name:        "Skill",
		Description: "desc",
		WhenToUse:   "",
		Steps:       []string{"step"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill when_to_use is required")
}

func TestNormalizeSkillSpec_MissingSteps(t *testing.T) {
	_, err := normalizeSkillSpec(&SkillSpec{
		Name:        "Skill",
		Description: "desc",
		WhenToUse:   "when",
		Steps:       nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill steps are required")
}

func TestNormalizeSkillSpec_TrimsAndFiltersSteps(t *testing.T) {
	got, err := normalizeSkillSpec(&SkillSpec{
		Name:        " My Skill ",
		Description: " desc ",
		WhenToUse:   " when ",
		Steps:       []string{"  step1 ", "", "  step2  "},
		Pitfalls:    []string{"  pit  ", "  "},
	})
	require.NoError(t, err)
	assert.Equal(t, "My Skill", got.Name)
	assert.Equal(t, "desc", got.Description)
	assert.Equal(t, "when", got.WhenToUse)
	assert.Equal(t, []string{"step1", "step2"}, got.Steps)
	assert.Equal(t, []string{"pit"}, got.Pitfalls)
}

// --- Tests for messageText (ContentParts extraction) ---

func TestMessageText_ContentFieldPreferred(t *testing.T) {
	msg := model.Message{Content: "direct content"}
	assert.Equal(t, "direct content", messageText(msg))
}

func TestMessageText_ContentPartsExtraction(t *testing.T) {
	text1 := "hello"
	text2 := "world"
	msg := model.Message{
		Content: "", // empty content
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text1},
			{Type: model.ContentTypeImage}, // non-text part, skipped
			{Type: model.ContentTypeText, Text: &text2},
		},
	}
	assert.Equal(t, "hello\nworld", messageText(msg))
}

func TestMessageText_EmptyContentAndNoParts(t *testing.T) {
	msg := model.Message{Content: ""}
	assert.Equal(t, "", messageText(msg))
}

// --- Tests for truncateMessageContent ---

func TestTruncateMessageContent_NonPositiveMaxDisabled(t *testing.T) {
	content := strings.Repeat("x", 100)
	assert.Equal(t, content, truncateMessageContent(content, 0))
	assert.Equal(t, content, truncateMessageContent(content, -5))
}

func TestTruncateMessageContent_ShortContentUnchanged(t *testing.T) {
	content := "short"
	assert.Equal(t, content, truncateMessageContent(content, 100))
}

func TestTruncateMessageContent_VerySmallMaxChars(t *testing.T) {
	content := strings.Repeat("x", 100)
	got := truncateMessageContent(content, 20)
	// maxChars < 32 means simple head-only truncation
	assert.Equal(t, 20, len(got))
	assert.Equal(t, strings.Repeat("x", 20), got)
}

func TestTruncateMessageContent_HeadTailWithPlaceholder(t *testing.T) {
	content := strings.Repeat("A", 100) + strings.Repeat("B", 100) + strings.Repeat("C", 100)
	got := truncateMessageContent(content, 100)
	assert.Contains(t, got, "AAA")
	assert.Contains(t, got, "CCC")
	assert.Contains(t, got, "chars omitted by reviewer transcript truncation")
}

func TestLLMReviewer_Review_SystemPromptIncludesFailureAwareGuidance(t *testing.T) {
	reviewModel := &recordingReviewModel{
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: `{"skip_reason":"x"}`}}},
		}},
	}
	reviewer := NewLLMReviewer(reviewModel)

	_, err := reviewer.Review(context.Background(), &ReviewInput{
		Transcript: []ReviewMessage{{Role: model.RoleUser, Content: "go"}},
	})
	require.NoError(t, err)
	system := reviewModel.request.Messages[0].Content

	assert.Contains(t, system, "Failure-aware learning")
	assert.Contains(t, system, "Never describe a step the transcript did not actually execute")
	assert.Contains(t, system, "On `fail` / `agent_error`")
	assert.Contains(t, system, "outcome `notes`")
}
