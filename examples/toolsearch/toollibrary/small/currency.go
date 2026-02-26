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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewCurrencyConverterTool creates a currency converter tool.
func NewCurrencyConverterTool() tool.CallableTool {
	return function.NewFunctionTool(
		convertCurrency,
		function.WithName("currency_converter"),
		function.WithDescription("Convert currency amounts between different currencies. Supports major currencies (USD, EUR, GBP, CNY, JPY, AUD, CAD, CHF). If exchange_rate is not provided, uses approximate rates for demonstration purposes only."),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Currency conversion request",
			Required:    []string{"amount", "from", "to"},
			Properties: map[string]*tool.Schema{
				"amount": {
					Type:        "number",
					Description: "The amount to convert",
				},
				"from": {
					Type:        "string",
					Description: "Source currency code (e.g., USD, EUR, CNY)",
				},
				"to": {
					Type:        "string",
					Description: "Target currency code (e.g., USD, EUR, CNY)",
				},
				"exchange_rate": {
					Type:        "number",
					Description: "Optional exchange rate (from to to). If not provided, uses approximate rates for demonstration.",
				},
			},
		}),
	)
}

type currencyRequest struct {
	Amount       float64 `json:"amount"`
	From         string  `json:"from"`
	To           string  `json:"to"`
	ExchangeRate float64 `json:"exchange_rate,omitempty"`
}

type currencyResponse struct {
	OriginalAmount  float64 `json:"original_amount"`
	FromCurrency    string  `json:"from_currency"`
	ToCurrency      string  `json:"to_currency"`
	ConvertedAmount float64 `json:"converted_amount"`
	ExchangeRate    float64 `json:"exchange_rate"`
	Message         string  `json:"message"`
}

func convertCurrency(_ context.Context, req currencyRequest) (currencyResponse, error) {
	exchangeRate := req.ExchangeRate

	if exchangeRate == 0 {
		rates := map[string]map[string]float64{
			"USD": {"EUR": 0.85, "GBP": 0.73, "CNY": 7.2, "JPY": 110.0, "AUD": 1.3, "CAD": 1.25, "CHF": 0.9},
			"EUR": {"USD": 1.18, "GBP": 0.86, "CNY": 8.47, "JPY": 129.5, "AUD": 1.53, "CAD": 1.47, "CHF": 1.06},
			"GBP": {"USD": 1.37, "EUR": 1.16, "CNY": 9.86, "JPY": 150.7, "AUD": 1.78, "CAD": 1.71, "CHF": 1.23},
			"CNY": {"USD": 0.14, "EUR": 0.12, "GBP": 0.10, "JPY": 15.28, "AUD": 0.18, "CAD": 0.17, "CHF": 0.13},
			"JPY": {"USD": 0.0091, "EUR": 0.0077, "GBP": 0.0066, "CNY": 0.065, "AUD": 0.012, "CAD": 0.011, "CHF": 0.0082},
		}

		if rateMap, ok := rates[req.From]; ok {
			if rate, ok := rateMap[req.To]; ok {
				exchangeRate = rate
			}
		}

		if exchangeRate == 0 {
			return currencyResponse{
				OriginalAmount:  req.Amount,
				FromCurrency:    req.From,
				ToCurrency:      req.To,
				ConvertedAmount: req.Amount,
				ExchangeRate:    1.0,
				Message:         "Exchange rate not found, returning original amount",
			}, fmt.Errorf("exchange rate not found for %s to %s", req.From, req.To)
		}
	}

	converted := req.Amount * exchangeRate

	return currencyResponse{
		OriginalAmount:  req.Amount,
		FromCurrency:    req.From,
		ToCurrency:      req.To,
		ConvertedAmount: converted,
		ExchangeRate:    exchangeRate,
		Message:         fmt.Sprintf("%.2f %s = %.2f %s (rate: %.4f)", req.Amount, req.From, converted, req.To, exchangeRate),
	}, nil
}
