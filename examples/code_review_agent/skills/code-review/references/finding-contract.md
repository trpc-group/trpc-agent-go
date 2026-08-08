# Finding contract

Each finding must refer to an added line in `work/review-input.json` and include:

- schema version `review/v1`;
- severity `critical`, `high`, `medium`, `low`, or `info`;
- confidence `high`, `medium`, or `low`;
- source `model`;
- a stable lowercase semantic anchor;
- a versioned rule ID ending in `/v1`; and
- concise evidence and a concrete recommendation.

Use low confidence when the evidence is incomplete. Low-confidence findings are
routed to human review. Never include credentials, tokens, private keys, or
other secrets in any field.
