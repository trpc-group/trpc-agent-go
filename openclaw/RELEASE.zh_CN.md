# OpenClaw 发布流程

OpenClaw 的预编译二进制通过本仓库的 GitHub Releases 发布，tag 格式为
`openclaw-vX.Y.Z`。

对应的 release workflow 在：

- `.github/workflows/openclaw-release.yml`

每次 release 会发布：

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

## 发一个 release

创建并推送 tag：

```bash
git tag openclaw-v0.0.1
git push origin openclaw-v0.0.1
```

workflow 会在匹配的原生 GitHub-hosted runner 上分别构建各个平台产物，
再汇总公共资源，并为这个 tag 创建或更新 GitHub Release。

你也可以使用 `workflow_dispatch` 手动触发，并传入 `version`，例如
`v0.0.1`。

## 本地 dry run

构建当前宿主机平台的包：

```bash
cd openclaw
bash ./release.sh build --version v0.0.1
```

在 `openclaw/dist/v0.0.1/` 下汇总共享资源：

```bash
cd openclaw
bash ./release.sh assemble --version v0.0.1
```

`build` 故意只面向当前宿主机。多架构 CGO 构建交给 CI，在对应的原生
runner 上执行，而不是在一台机器上做交叉编译。

## 安装入口

公网安装入口保持稳定：

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash
```

这个脚本会解析最新发布的 `openclaw-v*` GitHub Release，并下载与你当前机器
匹配的包。
