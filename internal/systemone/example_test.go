//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

func ExampleClient_SystemOne() {
	// Use a local fixture server so this example requires no credentials or model.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"example","answers":{"refund":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":0}}`)
	}))
	defer server.Close()
	client, err := systemone.NewClient(server.URL)
	if err != nil {
		panic(err)
	}
	type refundState struct {
		Message         string `json:"message"`
		DuplicateCharge bool   `json:"duplicate_charge"`
	}
	response, err := client.SystemOne(context.Background(), &systemone.Request{
		State: refundState{Message: "I was charged twice. Please refund me.", DuplicateCharge: true},
		Questions: map[string]systemone.Question{
			"refund": systemone.BinaryQuestion{Instructions: "Does the customer request a refund?"},
		},
	})
	if err != nil {
		panic(err)
	}
	answer, err := response.Binary("refund")
	if err != nil {
		panic(err)
	}
	fmt.Printf("P(refund requested): %.1f\n", answer.Probability)
	fmt.Println("Truncation known:", response.Usage.Truncated != nil)
	// Output:
	// P(refund requested): 0.9
	// Truncation known: false
}
