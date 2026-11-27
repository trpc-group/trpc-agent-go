//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone

// Request evaluates named questions against caller-supplied state. State is
// encoded once with encoding/json, including any custom MarshalJSON method;
// the result must be a JSON string, object, or array. Model overrides the client
// default when nonempty. Questions must contain at least one nonblank, valid
// UTF-8 ID. Request and question types are encoded by Client, not by marshaling
// Request directly. Callers must not mutate request data during a call.
type Request struct {
	Model     string
	State     any
	Questions map[string]Question
}

// Question is a BinaryQuestion, ChoiceQuestion, or ScoreQuestion. Both values
// and non-nil pointers are accepted; nil and other implementations are errors.
// Instructions must contain nonblank, valid UTF-8 text. The unexported method
// marks question types; callers do not implement protocol encoding.
type Question interface {
	isQuestion()
}

// BinaryQuestion asks for the probability that a proposition is true.
// Instructions must contain nonblank, valid UTF-8 text. Nil or empty Criteria
// uses the provider's default meanings for true and false.
type BinaryQuestion struct {
	Instructions string
	Criteria     *BinaryCriteria
}

func (BinaryQuestion) isQuestion() {}

// BinaryCriteria describes the true and false outcomes. Empty descriptions are
// omitted; nonempty descriptions must be valid UTF-8 and are sent unchanged.
type BinaryCriteria struct {
	True  string
	False string
}

// ChoiceQuestion selects one named option. Instructions must contain nonblank,
// valid UTF-8 text. Options must be nonempty and uniquely named. Their order is
// preserved on the wire because compatible servers can use positional input.
type ChoiceQuestion struct {
	Instructions string
	Options      []ChoiceOption
}

func (ChoiceQuestion) isQuestion() {}

// ChoiceOption defines a nonblank, valid UTF-8 label and optional description.
// Names are case-sensitive. An empty Description encodes as null, so the server
// uses the label alone. Nonempty descriptions must be valid UTF-8.
type ChoiceOption struct {
	Name        string
	Description string
}

// ScoreQuestion rates state against at least one ordered textual level.
// Instructions and every criterion must contain nonblank, valid UTF-8 text.
// Criteria positions define scores starting at zero; answers may be fractional.
// Provider-specific limits on the number of levels still apply.
type ScoreQuestion struct {
	Instructions string
	Criteria     []string
}

func (ScoreQuestion) isQuestion() {}
