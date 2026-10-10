//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package systemone_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// TestIntegrationJevExtended checks inputs that some compatible gateways reject.
// It uses the same JEV configuration as TestIntegrationJev and makes two calls.
// A rejection fails the test; it does not count as a pass or a skip.
func TestIntegrationJevExtended(t *testing.T) {
	config := integrationConfigFromEnvironment(t, "JEV")
	require.NotEmpty(t, config.apiKey, "JEV_API_KEY is required when JEV_BASE_URL is set")
	require.NotEmpty(t, config.model, "JEV_MODEL is required when JEV_BASE_URL is set")
	runIntegrationSuite(t, config, extendedIntegrationCases())
}

// TestIntegrationLayaExtended checks inputs that some compatible gateways reject.
// It uses the same LAYA configuration as TestIntegrationLaya and makes two calls.
// A rejection fails the test; it does not count as a pass or a skip.
func TestIntegrationLayaExtended(t *testing.T) {
	config := integrationConfigFromEnvironment(t, "LAYA")
	runIntegrationSuite(t, config, extendedIntegrationCases())
}

func extendedIntegrationCases() []integrationCase {
	return []integrationCase{
		{
			name: "ChoiceObjectArrayState",
			request: systemone.Request{
				State: []map[string]string{
					{"role": "user", "content": "I was charged twice."},
					{"role": "user", "content": "Please refund the duplicate payment."},
				},
				Questions: map[string]systemone.Question{"route": systemone.ChoiceQuestion{
					Instructions: "Choose the team that should handle this request.",
					Options: []systemone.ChoiceOption{
						{Name: "z_billing", Description: "Charges, invoices, and refunds"},
						{Name: "a_support", Description: "Technical troubleshooting"},
						{Name: "m_other", Description: "Other requests"},
					},
				}},
			},
		},
		{
			name: "ScoreSingleLevel",
			request: systemone.Request{
				State: "A routine billing question.",
				Questions: map[string]systemone.Question{"urgency": systemone.ScoreQuestion{
					Instructions: "Rate the request using the single available level.",
					Criteria:     []string{"Routine question"},
				}},
			},
		},
	}
}
