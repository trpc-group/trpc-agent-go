# OpenClaw release process

OpenClaw prebuilt binaries are published from this repo's GitHub
Releases, using tags in the form `openclaw-vX.Y.Z`.

The release workflow lives in:

- `.github/workflows/openclaw-release.yml`

Each release publishes:

- `openclaw-vX.Y.Z-linux-amd64.tar.gz`
- `openclaw-vX.Y.Z-linux-arm64.tar.gz`
- `openclaw-vX.Y.Z-darwin-amd64.tar.gz`
- `openclaw-vX.Y.Z-darwin-arm64.tar.gz`
- `checksums.txt`
- `VERSION`
- `openclaw-install.sh`
- `INSTALL.md`
- `INSTALL.zh_CN.md`
- `RELEASE.md`
- `RELEASE.zh_CN.md`

## Cut a release

Create and push a tag:

```bash
git tag openclaw-v0.0.1
git push origin openclaw-v0.0.1
```

The workflow builds each target on a matching native GitHub-hosted
runner, assembles the shared assets, and creates or updates the GitHub
release for that tag.

You can also run the workflow manually with `workflow_dispatch` and a
`version` input such as `v0.0.1`.

## Local dry run

Build a host-native archive:

```bash
cd openclaw
bash ./release.sh build --version v0.0.1
```

Assemble the shared assets in `openclaw/dist/v0.0.1/`:

```bash
cd openclaw
bash ./release.sh assemble --version v0.0.1
```

`build` intentionally targets only the current host. Multi-architecture
CGO builds are performed in CI on matching native runners instead of
cross-compiling from one machine.

## Installer entrypoint

The public installer stays stable at:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash
```

That script resolves the latest published `openclaw-v*` GitHub Release
and downloads the correct archive for the current machine.
