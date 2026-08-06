# Usage

The CLI loads this skill through `skill.NewFSRepository`; scripts are copied to
an isolated workspace before execution.

```bash
bash scripts/check_diff.sh work/input.diff
```

The script accepts exactly one workspace-relative unified diff path. It rejects
oversized input and prints aggregate JSON only. `go test`, `go vet`, and
optional `staticcheck` are orchestrated by the host wrapper because they need
the staged repository snapshot.
