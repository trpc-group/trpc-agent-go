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

Keep the pull request template and complete every applicable section.

The description must explain:

- **What changed**: the externally observable behavior, API, documentation, or
  example changes.
- **Why**: the problem, design motivation, and reason for selecting the proposed
  approach.
- **Public API and compatibility**: the affected public contracts and their
  compatibility implications.
- **Testing**: the automated and manual validation that was actually performed.
- **Release notes**: the user-visible effect of the change, or `NONE`.
- **Notes for reviewers**: areas that deserve focused review.

Do not leave template instructions unchanged. Do not submit a description that
only repeats the title or lists changed files without explaining their behavior
and motivation.

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
- keep provider-, vendor-, model-, protocol-, business-, and
  deployment-specific concepts out of shared packages unless they form a
  demonstrated general abstraction;
- avoid parallel entry points with substantially overlapping responsibilities;
- prefer the smallest surface that supports external consumers; and
- consider how the API can evolve without duplicate types, methods, or
  incompatible renames.

The pull request description must list every added or changed public symbol and
explain:

- why the public surface is necessary;
- why an existing API or extension point is insufficient;
- why the symbol belongs in its package;
- its default, zero-value, nil, error, ownership, and lifecycle behavior where
  relevant;
- its source, behavioral, serialization, persistence, and protocol
  compatibility where relevant; and
- any migration, deprecation, or future-extension considerations.

Every exported symbol must have meaningful Godoc that explains its contract.
Documentation that only restates the declaration is insufficient.

Public API naming is a framework design concern. Review exported names for
semantic accuracy, discoverability, package fit, abstraction boundaries, and
future evolution. Unexported naming and local refactoring preferences are not
public API concerns unless they are misleading or likely to cause incorrect
behavior.

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

### Release notes

Release notes are required for user-visible changes, including:

- critical bug fixes;
- notable features;
- deprecations or removals;
- public API or behavioral changes; and
- significant documentation additions.

Describe the impact from the user’s perspective and avoid unnecessary
implementation detail.

Use `NONE` for changes without user-visible impact, such as internal
refactoring or test-only changes.

### Updating a pull request

Push additional commits to the pull request branch when addressing feedback.

Both incremental commits and rebasing with a force-push are accepted. Keep the
pull request description synchronized with the final design and behavior,
especially after public APIs or compatibility decisions change.

## Copyright headers

Source files do not list individual author names. Contributor information is
preserved in project history instead.

New Go files must use the following header:

<!-- markdownlint-disable MD013 -->

```go
//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
```

<!-- markdownlint-enable MD013 -->

Use the year in which the file is added. Do not update the copyright year when
modifying an existing file.
