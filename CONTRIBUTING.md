# How to Contribute

Thank you for your interest in contributing to tRPC-Agent-Go.

We welcome bug fixes, features, tests, documentation, examples, design
improvements, and other contributions. Please read this guide before submitting
a change so that proposals, implementations, and reviews can proceed
consistently.

## Before contributing code

### Start with the issue tracker

Except for trivial changes, contributions should be associated with an existing
issue or a newly opened issue.

Starting with an issue allows maintainers and contributors to agree on the
problem, scope, and high-level design before implementation begins. It also
helps prevent duplicate work and avoids moving architectural discussions into
code review.

The issue tracker uses the following workflow labels:

- **NeedsInvestigation**: The problem is not yet sufficiently understood.
- **NeedsDecision**: The problem is understood, but the project has not selected
  an approach. Wait for a decision before starting implementation.
- **NeedsFix**: The problem and expected direction are sufficiently clear for
  implementation.

If an issue remains in `NeedsDecision` for some time, contributors may ask
maintainers for an update in the issue discussion.

### Report bugs with enough context

A bug report should answer the following questions:

1. Which version of tRPC-Agent-Go are you using?
2. Which operating system and processor architecture are you using?
3. What did you do?
4. What did you expect to happen?
5. What happened instead?

Include the relevant output of `go env` when environment details may affect the
problem.

### Discuss significant changes first

Significant features, framework abstractions, public API changes, persistence
changes, and protocol changes should be discussed before implementation.

High-level design should be settled in an issue or proposal. Code review should
verify and refine an agreed design, not become the first place where the design
is considered.

For larger change proposals, see
[Proposing Changes to tRPC](https://github.com/trpc-group/trpc/tree/main/proposal).

## Contributing code

Follow the
[GitHub flow](https://docs.github.com/en/get-started/quickstart/github-flow)
and submit changes through a pull request.

First-time contributors must sign the
[Tencent Contributor License Agreement](https://github.com/trpc-group/cla-database/blob/main/Tencent-Contributor-License-Agreement.md).
The pull request Conversation tab will provide signing instructions when
required.

### Language

Pull request titles and descriptions must be written in English.

Source-code comments and Go documentation comments must also be written in
English. Exceptions include translated documentation and test data that
intentionally verifies localized behavior.

Review discussions may use another language when that makes communication more
effective.

### Pull request title

Every pull request title must identify the primary affected package or
repository area and summarize the result of the change.

Prefer the following form for a change with one primary package:

```text
package: lowercase summary
```

When multiple packages are equally affected, enclose the package list in braces
and separate package names with a comma and a space:

```text
{package/a, package/b}: lowercase summary
```

Use one primary package whenever possible. Do not add documentation, tests, or
examples to the package list when they only support the primary implementation
change.

For a change that does not belong to a Go package, use the narrowest repository
area or subsystem that owns the change.

The summary should:

- begin with a lowercase letter;
- describe the result rather than the implementation process;
- be concise but meaningful without reading the diff; and
- complete the sentence “This change modifies tRPC-Agent-Go to _____.”

Use an ASCII colon followed by one space. A generic change type, issue number,
tool name, or bracketed tag does not replace the affected package name.

### Pull request description

Keep the description concise. The diff is the source of truth; the description
should provide the context that code alone cannot.

Use the pull request template to explain:

- **What changed**: the outcome and its user or developer impact.
- **Why**: the problem and any non-obvious design rationale.
- **Testing**: the validation that was actually performed.
- **Notes for reviewers**: optional risks or design decisions that deserve
  focused review.

Do not restate the implementation, enumerate every changed file or symbol, or
leave template instructions unchanged. When a public API, compatibility, or
migration concern is not clear from the diff, call it out under **Notes for
reviewers**.

Markdown is allowed in pull request descriptions.

Pull requests are squash-merged. The final commit description is composed from
the pull request title and description, while individual commit descriptions
are discarded. Write the title and description as durable project history.

### Public API and framework design

Treat every public API and externally observable framework behavior as a
long-lived compatibility commitment.

A public API change includes adding, removing, renaming, or changing:

- an exported type, function, method, interface, field, constant, or variable;
- an option, callback, plugin contract, or sentinel error;
- default, zero-value, nil, ownership, concurrency, or lifecycle behavior;
- serialized fields, persistence formats, or protocol contracts; or
- event ordering, streaming, cancellation, retry, or other observable behavior.

Before adding a public API:

- search for an existing API or extension point that can support the use case;
- verify that the concept belongs to the selected package and abstraction layer;
- keep implementation-specific concepts in their owning packages unless their
  semantics are genuinely shared across implementations;
- avoid parallel entry points with substantially overlapping responsibilities;
- prefer the smallest surface that supports external consumers; and
- consider how the API can evolve without duplicate types, methods, or
  incompatible renames.

Every exported symbol must have meaningful Godoc that explains its contract.
Documentation that only restates the declaration is insufficient.

Public API naming is a framework design concern. Review exported names for
semantic accuracy, discoverability, package fit, abstraction boundaries, and
future evolution. Unexported naming and local refactoring preferences are not
public API concerns unless they are misleading or likely to cause incorrect
behavior.

### Go conventions

Follow [Effective Go](https://go.dev/doc/effective_go) and the
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments), while
preserving established project APIs when compatibility requires it.

- Format code with `gofmt` and organize imports with `goimports`.
- Use short, lowercase, single-word package names. Avoid underscores, mixed
  capitals, and names that repeat their package when qualified.
- Use MixedCaps for Go names and spell common initialisms consistently, such as
  `ID`, `URL`, and `HTTP`.
- Make exported names read naturally with their package qualifier. Avoid
  stutter and redundant prefixes such as `pkg.PkgType`.
- Prefer small interfaces that describe behavior required by consumers. Do not
  introduce an interface only to anticipate hypothetical implementations or to
  make a concrete type easier to mock.
- Prefer returning concrete types from constructors. Add a constructor when it
  establishes invariants or improves usability; otherwise, make the zero value
  useful when practical.
- Pass `context.Context` as the first parameter when an operation needs it. Do
  not store a context in a struct unless the type explicitly represents that
  context's lifetime.
- Keep error strings lowercase and without trailing punctuation. Wrap errors
  with `%w` when callers need the cause, and expose sentinel or typed errors
  only when callers have a stable reason to inspect them.
- Make goroutine, channel, resource, cancellation, and shutdown ownership
  explicit. Background work must have a bounded way to stop.
- Write doc comments for exported declarations as complete sentences beginning
  with the declared name, and document behavior that callers must understand.

Do not turn idiomatic guidance into subjective churn. Match surrounding code
when several forms are valid, and do not refactor established public APIs only
to satisfy a style preference.

### Code quality and validation

Keep changes focused and avoid unrelated refactoring.

Before submitting or updating a pull request:

- format Go code with `gofmt`;
- organize imports with `goimports`;
- run tests for every affected module;
- run the root test suite when the root module is affected;
- add targeted tests for new behavior, boundary conditions, and regressions;
- update documentation and examples when public behavior changes; and
- verify that tests do not depend on credentials, external services,
  machine-specific paths, or unstable timing unless explicitly required.

Tests should validate externally observable behavior rather than merely execute
new code. Assertions should be strong enough to fail when the intended contract
is broken.

Report validation commands in a portable form. Do not include local absolute
paths, machine-specific cache directories, credentials, or developer-specific
environment configuration in pull request descriptions.

### Referencing issues

Use `Fixes #12345` when the pull request completely resolves an issue.

Use `Updates #12345` when the pull request contributes to an issue but does not
fully resolve it.

For an issue in another repository, use the full repository reference:

```text
Fixes owner/repository#12345
```

### Pull request type

Every pull request must have an appropriate type label:

- `type/bug`: fixes a newly discovered bug;
- `type/enhancement`: improves or refactors existing behavior;
- `type/feature`: adds new functionality;
- `type/documentation`: changes documentation;
- `type/api-change`: adds, removes, or changes a public API or contract;
- `type/failing-test`: addresses an intermittent or failing CI test;
- `type/performance`: improves performance; or
- `type/ci`: changes CI configuration or scripts.

Use `type/api-change` whenever the change affects a public API or externally
observable contract, even if another type label also applies.

### Updating a pull request

Push additional commits to the pull request branch when addressing feedback.

Both incremental commits and rebasing with a force-push are accepted. Keep the
pull request description synchronized with the final outcome and any important
review context.

## Copyright headers

Source files do not list individual author names. Contributor information is
preserved in project history instead.

New Go files must use the following header:

<!-- markdownlint-disable MD013 -->

```go
//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) <YEAR> Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
```

<!-- markdownlint-enable MD013 -->

Use the year in which the file is added. Do not update the copyright year when
modifying an existing file.
