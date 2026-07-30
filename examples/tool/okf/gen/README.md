# Generate an OKF Bundle

A minimal demo of producing an
[OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog) bundle.

Generating a bundle (from notes, a DB schema, an iWiki space, ...) is offline
content production — not an agent-runtime concern — so it lives here as an
example, not in the framework. The framework ships the read/validate side
(`tool/okf`); you write concepts with plain YAML + files.

The flow:

1. draft a few concepts (reusing `okf.Frontmatter` so the YAML shape matches the reader);
2. write them as markdown with YAML frontmatter + a root `index.md`;
3. lint with `okf.Validate` — the strict, producer/CI-side conformance gate
   (the counterpart to the runtime tolerance a consumer must have).

```bash
cd examples/tool/okf/gen
go run .
```

To build a real bundle from your own source, replace the hard-coded `drafts`
with your enumeration logic (and optionally an LLM enrichment pass over each
body). Concept IDs must be clean, bundle-relative, slash-separated paths; the
example rejects absolute or escaping paths before writing any concept file.
