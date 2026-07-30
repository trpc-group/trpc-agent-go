# Basic OKF Usage

This example consumes the static OKF bundle in `bundle/`. It demonstrates:

- opening a directory-backed store with `localokf.New`;
- progressively listing the bundle and reading a concept;
- adapting the store with `okf.NewToolSet`;
- mounting the resulting `okf_list` and `okf_read` tools on an LLM agent.

No model credentials are needed because the example only wires and displays the
tools; it does not run an LLM request.

```bash
cd examples/tool/okf/basic
go run .
```

Use another local bundle with:

```bash
go run . -bundle /path/to/bundle
```
