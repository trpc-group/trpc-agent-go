<!-- markdownlint-disable MD041 -->

<!--
Write the pull request title and description in English.

The title must identify the primary affected package or repository area.

Preferred form:
  package: lowercase summary

For multiple equally affected packages:
  {package/a, package/b}: lowercase summary
-->

## What changed

Describe the externally observable behavior, API, documentation, or example
changes. Do not only list changed files.

## Why

Explain the problem, design motivation, and why this approach was selected.

## Public API and compatibility

List every added, removed, renamed, or changed public API or externally
observable contract.

For each change, explain its necessity, package ownership, relationship to
existing APIs, default behavior, and compatibility implications.

Write `None` if this pull request does not change public APIs or externally
observable contracts.

## Testing

List the automated and manual validation that was actually performed. If a
check is not applicable, explain why.

## Release notes

Describe the user-visible effect of this change. Write `NONE` if there is no
user-visible effect.

## Notes for reviewers

Call out areas that deserve focused review, such as public API design,
concurrency, persistence, event ordering, streaming, cancellation, protocol
compatibility, security boundaries, or provider-specific behavior.
