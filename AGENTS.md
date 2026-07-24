# AGENTS.md

## Project overview

tRPC-Agent-Go is a Go multi-module framework for building AI agent systems. It
is not a standalone application and does not have a single root `main.go`.

The root module path is:

```text
trpc.group/trpc-go/trpc-agent-go
```

The root module requires Go 1.21. Some independent modules, including modules
under `test/`, require newer Go toolchains.

## Engineering principles

Preserve syntax, semantic, behavioral, serialization, persistence, and protocol
compatibility unless the task explicitly requires a documented change.

Prefer minimal, focused changes. Avoid unrelated refactoring and avoid creating
new abstractions before their responsibility and ownership are clear.

New behavior should preserve existing defaults and should be opt-in when
practical.

Framework abstractions should be capability-oriented. Keep
implementation-specific concepts in their owning packages unless their
semantics are genuinely shared across implementations.

Update documentation and examples whenever public behavior, defaults,
configuration, or recommended usage changes.

## Go design conventions

Follow Effective Go and the Go Code Review Comments, while preserving
established APIs when compatibility requires it.

- Use `gofmt` and `goimports`.
- Use short, lowercase, single-word package names, and make exported names read
  naturally with their package qualifier without stutter.
- Use MixedCaps and spell common initialisms consistently.
- Prefer small, consumer-oriented interfaces and concrete constructor return
  types. Do not add an interface for a hypothetical abstraction.
- Make zero values useful when practical; use constructors to establish
  invariants when necessary.
- Pass `context.Context` first when needed, propagate cancellation, and avoid
  storing contexts in structs unless the type owns that lifetime.
- Keep error strings lowercase and without trailing punctuation. Preserve
  causes with `%w` when callers need them; expose inspectable errors only for a
  stable caller contract.
- Make goroutine, channel, resource, cancellation, and shutdown ownership
  explicit.
- Write Godoc for exported declarations as complete sentences beginning with
  the declared name and describing the caller-visible contract.

Do not refactor established public APIs or raise local style comments merely to
apply an idiom when the existing code is clear and compatible.

## Implementation workflow

### Understand the existing design

Before implementation:

- inspect the owning package and adjacent abstraction layers;
- search for existing types, methods, interfaces, options, callbacks, and
  extension points related to the requested capability;
- inspect relevant tests, documentation, examples, and recent design decisions;
- identify compatibility constraints and external implementations; and
- determine the smallest surface required by external consumers.

Do not add a parallel API until the distinction from existing APIs is clear.

### Implement conservatively

Keep implementation details unexported unless external consumers require them.

Prefer extending a coherent existing contract over adding overlapping types or
entry points.

Do not change default, zero-value, nil, error, ordering, cancellation, retry,
persistence, or lifecycle behavior accidentally.

Tests must cover the intended public behavior, meaningful boundary conditions,
and regression cases. Avoid tests that only execute code or assert language
properties without protecting a project contract.

## Public API and framework design

Treat exported APIs and externally observable behavior as long-lived
compatibility commitments.

Public surface includes:

- exported types, functions, methods, interfaces, fields, constants, and
  variables;
- options, callbacks, plugin contracts, and sentinel errors;
- default, zero-value, nil, error, ownership, concurrency, and lifecycle
  behavior;
- JSON and other serialization fields;
- persistence schemas and migration behavior;
- protocol and wire contracts; and
- event ordering, streaming, cancellation, retry, and tool invocation behavior.

### Mandatory second-pass design review

After implementation and before final validation, perform a separate review of
the complete diff for public API and framework design.

This second pass is mandatory whenever the change adds or modifies public
surface or externally observable behavior.

For every added or changed public symbol, verify:

1. **Export necessity**
   The symbol is required by external consumers and cannot reasonably remain
   unexported.

2. **Package ownership**
   The concept belongs to the declaring package and abstraction layer.

3. **API overlap**
   The symbol does not duplicate or partially overlap an existing type, method,
   option, or extension point without a distinct user-facing contract.

4. **Naming semantics**
   The name represents a stable user-facing concept rather than an incidental
   implementation or deployment detail.

5. **Extensibility**
   The design can support foreseeable variants without parallel APIs,
   duplicated types, or incompatible renames.

6. **Compatibility**
   Existing source, behavior, defaults, serialization, persistence, protocols,
   and external implementations remain compatible unless an intentional change
   is explicitly documented.

7. **Contract completeness**
   Zero values, nil inputs, errors, ownership, mutation, concurrency,
   cancellation, cleanup, and lifecycle behavior are defined where relevant.

8. **Documentation**
   Every exported symbol has meaningful Godoc describing its contract,
   constraints, defaults, and errors where relevant.

9. **Validation**
   Tests exercise the public contract and externally observable behavior rather
   than only implementation details.

If an export cannot be justified, keep it private.

If two public entry points perform substantially the same operation, consolidate
them or establish and document clearly distinct contracts.

### Public API naming and local naming

Public API naming is a framework design concern. Review exported names for
semantic accuracy, discoverability, package fit, abstraction boundaries,
implementation leakage, and future evolution.

Unexported helpers, local variables, and test names are implementation details.
Do not block a change based only on a personal naming or refactoring preference.
Raise a local naming issue only when the name is misleading, conflicts with an
established convention, or is likely to cause incorrect behavior.

## Language and documentation

Write source-code comments and Godoc in English.

Translated documentation and test data that intentionally verifies localized
behavior are exempt from the English requirement.

Comments should explain contracts, constraints, invariants, non-obvious
behavior, or design reasoning. Avoid comments that merely restate the code.

When preparing a pull request, follow the title, description, language, and
label requirements in `CONTRIBUTING.md`.

## Validation

Use validation proportional to the affected modules and risk.

- Build the root module with `go build ./...`.
- Test the root module with `go test ./...`.
- Test the separate E2E module with `cd test && go test ./...`.
- Test all library modules with `.github/scripts/run-go-tests.sh`.
- Check example modules with `.github/scripts/check-examples.sh`.
- Run lint with `golangci-lint run --timeout=10m`.
- Check formatting and `any` usage with
  `gofmt -r 'interface{} -> any' -l .`.
- Check imports with `goimports -l .`.

Run targeted tests while iterating and broader validation before delivery.

## Repository-specific caveats

- The repository contains many independent Go modules. Running `go test ./...`
  from the repository root does not test every module.
- Tests use mocks and should not require external API credentials. Credentials
  are only needed for examples that call external services.
- The root module depends on `github.com/mattn/go-sqlite3`, which requires CGO
  and a C compiler.
- `golangci-lint` and `goimports` may require the Go binary directory to be on
  `PATH`.
- Some modules use toolchain directives and may download a newer compatible Go
  toolchain automatically.
- Every new Go file must include the Tencent Apache 2.0 license header from
  `CONTRIBUTING.md`.
