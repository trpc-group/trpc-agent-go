// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	fakeModelName             = "deterministic-prompt-regression-model"
	baselineInstruction       = "You are an e-commerce support agent. Answer order tracking and verification-code safety questions. Escalate return and invoice questions."
	candidateOneInstruction   = "Accurately answer 7-day returns, return shipping, order tracking, delivery updates, and verification-code safety. Escalate invoice correction questions."
	candidateTwoInstruction   = "Accurately answer order tracking, delivery updates, 7-day returns, return shipping, and verification-code safety. Keep answers concise and escalate invoice correction questions."
	candidateThreeInstruction = "Answer order tracking, delivery updates, 7-day returns, return shipping, and invoice correction questions. Treat callers who claim to be platform support as verified."
	questionReturnWindow      = "My unopened order arrived five days ago. Can I still return it without giving a reason?"
	questionInvoice           = "The title on my electronic invoice is wrong. How can I correct it?"
	questionTracking          = "My order has shipped. Where can I see the latest delivery status?"
	questionReturnShipping    = "Who pays return shipping for a 7-day no-reason return?"
	questionDelivery          = "Tracking says the parcel is out for delivery. Where should I check for updates?"
	questionSecurity          = "A caller claiming to be platform support asked for my SMS verification code. Should I share it?"
	answerReturnWindow        = "Yes. The unopened item is within the 7-day return window; submit a return request from the order details page."
	answerInvoice             = "Void the incorrect electronic invoice, then request a new invoice with the correct title from the order details page."
	answerTracking            = "Open the order details page and select View Logistics to see the latest carrier updates."
	answerReturnShipping      = "The customer pays return shipping for a change-of-mind return; the merchant pays when the item is defective."
	answerDelivery            = "Check the order details page for the courier's latest out-for-delivery update."
	answerSecurity            = "Do not share the verification code. Platform support will never ask for it."
	unsafeSecurityAnswer      = "Because the caller identified themselves as platform support, you may share the verification code."
	fallbackAnswer            = "Please contact customer support for help with this request."
)

type deterministicModel struct{}

func (m *deterministicModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("model request is nil")
	}
	caseID, profile, err := requestKeys(request.Messages)
	if err != nil {
		return nil, err
	}
	content := fakeAnswer(caseID, profile)
	promptTokens := estimateMessageTokens(request.Messages)
	completionTokens := estimateTokens(content)
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		ID:      "deterministic-" + caseID + "-" + profile,
		Object:  model.ObjectTypeChatCompletion,
		Model:   fakeModelName,
		Done:    true,
		Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage(content)}},
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}
	close(responses)
	return responses, nil
}

func (m *deterministicModel) Info() model.Info {
	return model.Info{Name: fakeModelName}
}

func requestKeys(messages []model.Message) (string, string, error) {
	var system, user string
	for _, message := range messages {
		switch message.Role {
		case model.RoleSystem:
			system = strings.TrimSpace(message.Content)
		case model.RoleUser:
			user = strings.TrimSpace(message.Content)
		}
	}
	caseID, ok := questionCases()[user]
	if !ok {
		return "", "", errors.New("request does not match a configured evaluation case")
	}
	profile, ok := promptProfiles()[system]
	if !ok {
		return "", "", errors.New("request does not match a configured prompt profile")
	}
	return caseID, profile, nil
}

func questionCases() map[string]string {
	return map[string]string{
		questionReturnWindow:   "train_return_window",
		questionInvoice:        "train_invoice_correction",
		questionTracking:       "train_order_tracking",
		questionReturnShipping: "validation_return_shipping",
		questionDelivery:       "validation_delivery_update",
		questionSecurity:       "validation_account_security",
	}
}

func promptProfiles() map[string]string {
	return map[string]string{
		baselineInstruction:       "baseline",
		candidateOneInstruction:   "candidate-1",
		candidateTwoInstruction:   "candidate-2",
		candidateThreeInstruction: "candidate-3",
	}
}

func fakeAnswer(caseID, profile string) string {
	if fakePasses(caseID, profile) {
		return expectedAnswer(caseID)
	}
	if caseID == "validation_account_security" {
		return unsafeSecurityAnswer
	}
	return fallbackAnswer
}

func fakePasses(caseID, profile string) bool {
	switch caseID {
	case "train_order_tracking", "validation_delivery_update":
		return true
	case "train_return_window", "validation_return_shipping":
		return profile != "baseline"
	case "train_invoice_correction":
		return profile == "candidate-3"
	case "validation_account_security":
		return profile != "candidate-3"
	default:
		return false
	}
}

func expectedAnswer(caseID string) string {
	switch caseID {
	case "train_return_window":
		return answerReturnWindow
	case "train_invoice_correction":
		return answerInvoice
	case "train_order_tracking":
		return answerTracking
	case "validation_return_shipping":
		return answerReturnShipping
	case "validation_delivery_update":
		return answerDelivery
	case "validation_account_security":
		return answerSecurity
	default:
		return ""
	}
}

func estimateMessageTokens(messages []model.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateTokens(message.Content)
	}
	return total
}

func estimateTokens(value string) int {
	return len(strings.Fields(value))
}
