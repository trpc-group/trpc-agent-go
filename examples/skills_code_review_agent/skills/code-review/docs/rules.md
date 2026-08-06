# Go Review Rules

## Security

- `SEC001`: reject hard-coded API keys, tokens, passwords, private keys, and
  credential-bearing URLs. Evidence must contain `[REDACTED]`.
- `SEC002`: flag `exec.Command` calls that invoke a shell with `-c`.
- `SEC003`: flag SQL assembled with string concatenation or `fmt.Sprintf`.

## Concurrency And Context

- `CON001`: a new goroutine must have visible cancellation and an ownership
  path that joins it during shutdown.
- `CON002`: call cancellation functions returned by `context.WithCancel`,
  `context.WithTimeout`, and `context.WithDeadline`.

## Resource Lifecycle

- `RES001`: close files and HTTP response bodies on every path after the open
  operation succeeds.
- `DB002`: close query rows after checking the query error.
- `DB003`: assign ownership of `sql.DB` and close it during shutdown.

## Database Transactions

- `DB001`: install a rollback guard immediately after `Begin` or `BeginTx`
  succeeds. Commit only after all operations succeed.

## Error Handling

- `ERR001`: do not discard an error or return a success zero value from an
  error branch. Wrap errors with operation context and `%w` where applicable.

## Tests And Confidence

- `TST001`: changed non-test Go code without a same-package test change is a
  low-confidence warning. It requires human review and is not a confirmed
  finding.
- Findings at confidence `0.80` or higher are confirmed. Lower-confidence
  observations go to warnings and `needs_human_review`.
- Results are deduplicated by file, changed line, and category. The strongest
  result wins.
