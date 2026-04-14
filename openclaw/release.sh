#!/usr/bin/env bash
set -euo pipefail

readonly DEFAULT_DIST_DIR_NAME="dist"
readonly INSTALL_SCRIPT_NAME="openclaw-install.sh"
readonly CHECKSUMS_FILE_NAME="checksums.txt"
readonly VERSION_FILE_NAME="VERSION"
readonly PACKAGE_ROOT_NAME="openclaw"
readonly BINARY_NAME="openclaw"
readonly SQLITE_BACKEND_VALUE="enabled"

usage() {
  cat <<'EOF'
Build OpenClaw release assets.

Usage:
  release.sh build --version <version> [options]
  release.sh assemble --version <version> [options]

Commands:
  build
      Build one release archive for the current host target.

  assemble
      Generate checksums and copy install/docs assets into dist/<version>.

Options:
  --version <version>      Release version, for example v0.0.1.
  --target <os/arch>       Target triple. Default: current host target.
  --dist-dir <dir>         Dist directory. Default: openclaw/dist
  --go-bin <path>          Go binary to use.
  -h, --help               Show help.
EOF
}

log() {
  printf '%s\n' "$*"
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "missing required command: $1"
  fi
}

normalize_version() {
  local value="$1"

  value="$(printf '%s' "$value" | tr -d '[:space:]')"
  value="${value#openclaw-}"
  if [ -z "$value" ]; then
    printf '%s' ""
    return
  fi
  case "$value" in
    v*)
      printf '%s' "$value"
      ;;
    *)
      printf 'v%s' "$value"
      ;;
  esac
}

repo_root() {
  git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel
}

module_dir() {
  printf '%s/openclaw' "$(repo_root)"
}

dist_dir() {
  local base_dir="$1"
  local version="$2"

  printf '%s/%s' "$base_dir" "$version"
}

pick_go_bin() {
  local candidate="$1"

  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    printf '%s' "$candidate"
    return
  fi
  if [ -n "${OPENCLAW_GO_BIN:-}" ] && \
    [ -x "${OPENCLAW_GO_BIN}" ]; then
    printf '%s' "${OPENCLAW_GO_BIN}"
    return
  fi
  command -v go
}

host_target() {
  local go_bin="$1"
  local goos goarch

  goos="$("$go_bin" env GOOS)"
  goarch="$("$go_bin" env GOARCH)"
  printf '%s/%s' "$goos" "$goarch"
}

archive_name() {
  local version="$1"
  local goos="$2"
  local goarch="$3"

  printf '%s-%s-%s-%s.tar.gz' \
    "$PACKAGE_ROOT_NAME" "$version" "$goos" "$goarch"
}

write_metadata() {
  local output="$1"
  local version="$2"
  local goos="$3"
  local goarch="$4"
  local go_version="$5"
  local commit

  commit="$(git -C "$(repo_root)" rev-parse HEAD)"
  cat >"$output" <<EOF
PACKAGE_VERSION='${version}'
PACKAGE_GOOS='${goos}'
PACKAGE_GOARCH='${goarch}'
PACKAGE_GO_VERSION='${go_version}'
SQLITE_BACKEND='${SQLITE_BACKEND_VALUE}'
SOURCE_COMMIT='${commit}'
EOF
}

smoke_test_host_binary() {
  local binary_path="$1"

  "$binary_path" version >/dev/null
}

build_release() {
  local version="$1"
  local target="$2"
  local dist_root="$3"
  local go_bin="$4"
  local host goos goarch stage_dir archive output_dir go_version

  [ -n "$version" ] || die "--version is required"

  host="$(host_target "$go_bin")"
  if [ -z "$target" ]; then
    target="$host"
  fi
  if [ "$target" != "$host" ]; then
    die "cross-building ${target} requires a matching native runner"
  fi

  goos="${target%/*}"
  goarch="${target#*/}"
  go_version="$("$go_bin" version | awk '{ print $3 }')"
  output_dir="$(dist_dir "$dist_root" "$version")"
  stage_dir="${output_dir}/_stage/${PACKAGE_ROOT_NAME}"
  archive="$(archive_name "$version" "$goos" "$goarch")"

  require_cmd "$go_bin"
  require_cmd git
  require_cmd tar
  rm -rf "$stage_dir"
  mkdir -p "${stage_dir}/bin"
  mkdir -p "${stage_dir}/config"

  (
    cd "$(module_dir)"
    CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
      "$go_bin" build -trimpath \
      -ldflags "-X main.releaseVersion=${version}" \
      -o "${stage_dir}/bin/${BINARY_NAME}" \
      ./cmd/openclaw
  )

  smoke_test_host_binary "${stage_dir}/bin/${BINARY_NAME}"

  cp "$(module_dir)/openclaw.stdin.yaml" \
    "${stage_dir}/config/openclaw.stdin.yaml"
  cp "$(module_dir)/openclaw.stdin.sqlite.yaml" \
    "${stage_dir}/config/openclaw.stdin.sqlite.yaml"
  cp "$(module_dir)/openclaw.yaml" \
    "${stage_dir}/config/openclaw.telegram.yaml"
  cp -R "$(module_dir)/skills" "${stage_dir}/skills"
  cp "$(module_dir)/INSTALL.md" "${stage_dir}/README.md"
  write_metadata \
    "${stage_dir}/metadata.env" \
    "$version" \
    "$goos" \
    "$goarch" \
    "$go_version"

  mkdir -p "$output_dir"
  (
    cd "${output_dir}/_stage"
    tar -czf "${output_dir}/${archive}" "${PACKAGE_ROOT_NAME}"
  )
  rm -rf "${output_dir}/_stage"

  log "built ${archive}"
}

write_checksums() {
  local output_dir="$1"
  local checksum_cmd

  if command -v sha256sum >/dev/null 2>&1; then
    checksum_cmd="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    checksum_cmd="shasum -a 256"
  else
    die "missing sha256sum or shasum"
  fi

  (
    cd "$output_dir"
    eval "$checksum_cmd" ./*.tar.gz | \
      sed 's#  \./#  #' > "${CHECKSUMS_FILE_NAME}"
  )
}

assemble_release() {
  local version="$1"
  local dist_root="$2"
  local output_dir

  [ -n "$version" ] || die "--version is required"

  output_dir="$(dist_dir "$dist_root" "$version")"
  [ -d "$output_dir" ] || \
    die "missing ${output_dir}; run build first"
  ls "${output_dir}"/*.tar.gz >/dev/null 2>&1 || \
    die "no release archives found under ${output_dir}"

  write_checksums "$output_dir"
  printf '%s\n' "$version" >"${output_dir}/${VERSION_FILE_NAME}"
  cp "$(module_dir)/install.sh" \
    "${output_dir}/${INSTALL_SCRIPT_NAME}"
  cp "$(module_dir)/INSTALL.md" "${output_dir}/INSTALL.md"
  cp "$(module_dir)/INSTALL.zh_CN.md" "${output_dir}/INSTALL.zh_CN.md"
  cp "$(module_dir)/RELEASE.md" "${output_dir}/RELEASE.md"
  cp "$(module_dir)/RELEASE.zh_CN.md" "${output_dir}/RELEASE.zh_CN.md"

  log "assembled assets under ${output_dir}"
}

main() {
  local command="${1:-}"
  local version=""
  local target=""
  local dist_root="$(module_dir)/${DEFAULT_DIST_DIR_NAME}"
  local go_bin=""

  if [ "$#" -eq 0 ]; then
    usage
    exit 1
  fi
  shift

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version)
        version="$(normalize_version "${2:-}")"
        shift 2
        ;;
      --target)
        target="${2:-}"
        shift 2
        ;;
      --dist-dir)
        dist_root="${2:-}"
        shift 2
        ;;
      --go-bin)
        go_bin="${2:-}"
        shift 2
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      *)
        die "unknown option: $1"
        ;;
    esac
  done

  go_bin="$(pick_go_bin "$go_bin")"

  case "$command" in
    build)
      build_release "$version" "$target" "$dist_root" "$go_bin"
      ;;
    assemble)
      assemble_release "$version" "$dist_root"
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
