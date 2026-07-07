# okf-gen — generate an OKF bundle

A minimal demo of **producing** an [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog)
bundle and consuming it back.

Generating a bundle (from notes, a DB schema, an iWiki space, ...) is offline
content production — not an agent-runtime concern — so it lives here as an
example, not in the framework. The framework ships the read/validate side
(`tool/okf`); you write concepts with plain YAML + files.

The flow:

1. draft a few concepts (reusing `okf.Frontmatter` so the YAML shape matches the reader);
2. write them as markdown with YAML frontmatter + a root `index.md`;
3. lint with `okf.Validate` — the strict, producer/CI-side conformance gate
   (the counterpart to the runtime tolerance a consumer must have);
4. read one concept back through `localokf` (the tolerant runtime consumer).

```bash
go run .
```

To build a real bundle from your own source, replace the hard-coded `drafts`
with your enumeration logic (and optionally an LLM enrichment pass over each
body). To point an agent at a bundle, use `okf.NewToolSet(localokf.New(dir))`.
