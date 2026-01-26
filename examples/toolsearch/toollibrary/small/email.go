//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewEmailValidatorTool creates an email validator tool.
func NewEmailValidatorTool() tool.CallableTool {
	return function.NewFunctionTool(
		validateEmail,
		function.WithName("email_validator"),
		function.WithDescription("Validate email addresses. Checks format and extracts username and domain parts. Returns validation result and email components."),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Email validation request",
			Required:    []string{"email"},
			Properties: map[string]*tool.Schema{
				"email": {
					Type:        "string",
					Description: "Email address to validate",
				},
			},
		}),
	)
}

type emailRequest struct {
	Email string `json:"email"`
}

type emailResponse struct {
	Email    string `json:"email"`
	Valid    bool   `json:"valid"`
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Message  string `json:"message"`
}

func validateEmail(_ context.Context, req emailRequest) (emailResponse, error) {
	email := strings.TrimSpace(req.Email)

	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	valid := emailRegex.MatchString(email)

	var username, domain string
	if strings.Contains(email, "@") {
		parts := strings.Split(email, "@")
		if len(parts) == 2 {
			username = parts[0]
			domain = parts[1]
		}
	}

	message := "Invalid email address"
	if valid {
		message = "Valid email address"
	}

	return emailResponse{
		Email:    email,
		Valid:    valid,
		Username: username,
		Domain:   domain,
		Message:  message,
	}, nil
}
