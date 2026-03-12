#!/usr/bin/env bash
set -euo pipefail

readonly DEFAULT_RELEASE_REPO="trpc-group/trpc-agent-go"
readonly DEFAULT_API_BASE_URL="https://api.github.com"
readonly DEFAULT_DOWNLOAD_BASE_URL="https://github.com"
readonly DEFAULT_RELEASE_PREFIX="openclaw-"
readonly DEFAULT_PROFILE="stdin"
readonly DEFAULT_CONFIG_ROOT_DIR=".trpc-agent-go-github"
readonly DEFAULT_BIN_SUBDIR=".local/bin"
readonly DEFAULT_CONFIG_SUBDIR="${DEFAULT_CONFIG_ROOT_DIR}/openclaw"
readonly DEFAULT_STATE_SUBDIR="${DEFAULT_CONFIG_ROOT_DIR}/openclaw"

readonly PROFILE_DIR_NAME="profiles"
readonly BUNDLED_SKILLS_DIR_NAME="bundled-skills"
readonly MANAGED_SKILLS_DIR_NAME="skills"
readonly INSTALL_METADATA_FILE_NAME=".openclaw-install.env"

readonly PACKAGE_ROOT_NAME="openclaw"
readonly BINARY_NAME="openclaw"
readonly CHECKSUMS_FILE_NAME="checksums.txt"

readonly profileStdin="stdin"
readonly profileStdinSQLite="stdin-sqlite"
readonly profileTelegram="telegram"

readonly githubTokenEnvName="GITHUB_TOKEN"
readonly ghTokenEnvName="GH_TOKEN"

usage() {
  cat <<'EOF'
Install the OpenClaw prebuilt release from GitHub Releases.

Usage:
  install.sh [options]

Options:
  --version <version>          Install a specific version, for example
                               v0.0.1 or 0.0.1.
  --profile <name>             Config profile:
                               stdin | stdin-sqlite | telegram
  --repo <owner/name>          GitHub repository. Default:
                               trpc-group/trpc-agent-go
  --api-base-url <url>         GitHub API base URL. Default:
                               https://api.github.com
  --download-base-url <url>    GitHub download base URL. Default:
                               https://github.com
  --bin-dir <dir>              Install the binary to this directory.
  --config-dir <dir>           Install config files to this directory.
  --state-dir <dir>            Install bundled skills to this state dir.
  --force-config               Overwrite openclaw.yaml with the selected
                               profile.
  -h, --help                   Show help.
EOF
}

log() {
  printf '%s\n' "$*"
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

warn() {
  printf 'warning: %s\n' "$*" >&2
}

resolve_dir_path() {
  local dir="$1"

  mkdir -p "$dir"
  (
    cd "$dir"
    pwd -L
  )
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "missing required command: $1"
  fi
}

trim_trailing_slash() {
  local value="$1"

  printf '%s' "${value%/}"
}

normalize_version() {
  local value="$1"

  value="$(printf '%s' "$value" | tr -d '[:space:]')"
  value="${value#${DEFAULT_RELEASE_PREFIX}}"
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

release_tag() {
  local version="$1"

  printf '%s%s' "$DEFAULT_RELEASE_PREFIX" "$(normalize_version "$version")"
}

detect_os() {
  case "$(uname -s)" in
    Linux)
      printf 'linux'
      ;;
    Darwin)
      printf 'darwin'
      ;;
    *)
      die "unsupported OS: $(uname -s)"
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64)
      printf 'amd64'
      ;;
    arm64 | aarch64)
      printf 'arm64'
      ;;
    *)
      die "unsupported architecture: $(uname -m)"
      ;;
  esac
}

profile_file_name() {
  case "$1" in
    "$profileStdin")
      printf 'openclaw.stdin.yaml'
      ;;
    "$profileStdinSQLite")
      printf 'openclaw.stdin.sqlite.yaml'
      ;;
    "$profileTelegram")
      printf 'openclaw.telegram.yaml'
      ;;
    *)
      die "unsupported profile: $1"
      ;;
  esac
}

download_text() {
  local url="$1"
  local token

  token="$(github_token)"
  if [ -n "$token" ]; then
    curl -fsSL --retry 3 \
      -H "Authorization: Bearer ${token}" \
      "$url"
    return
  fi

  curl -fsSL --retry 3 "$url"
}

download_file() {
  local url="$1"
  local output="$2"
  local token

  token="$(github_token)"
  if [ -n "$token" ]; then
    curl -fsSL --retry 3 \
      -H "Authorization: Bearer ${token}" \
      "$url" \
      -o "$output"
    return
  fi

  curl -fsSL --retry 3 "$url" -o "$output"
}

github_token() {
  if [ -n "${!ghTokenEnvName:-}" ]; then
    printf '%s' "${!ghTokenEnvName}"
    return
  fi

  if [ -n "${!githubTokenEnvName:-}" ]; then
    printf '%s' "${!githubTokenEnvName}"
    return
  fi

  printf '%s' ""
}

extract_latest_tag_from_json() {
  local payload="$1"
  local prefix="$2"

  printf '%s\n' "$payload" | tr '\n' ' ' | grep -o \
    "\"tag_name\"[[:space:]]*:[[:space:]]*\"${prefix}v[^\"]*\"" | \
    head -n 1 | sed -E 's/.*"([^"]+)"/\1/'
}

extract_latest_tag_from_atom() {
  local payload="$1"
  local prefix="$2"

  printf '%s\n' "$payload" | tr '\n' ' ' | grep -o \
    "<title>${prefix}v[^<]*</title>" | \
    head -n 1 | sed -E 's#<title>([^<]+)</title>#\1#'
}

release_api_url() {
  local api_base_url="$1"
  local repo="$2"

  printf '%s/repos/%s/releases?per_page=100' \
    "$(trim_trailing_slash "$api_base_url")" \
    "$repo"
}

release_feed_url() {
  local download_base_url="$1"
  local repo="$2"

  printf '%s/%s/releases.atom' \
    "$(trim_trailing_slash "$download_base_url")" \
    "$repo"
}

resolve_version() {
  local requested="$1"
  local repo="$2"
  local api_base_url="$3"
  local download_base_url="$4"
  local payload tag

  if [ -n "$requested" ]; then
    printf '%s' "$(normalize_version "$requested")"
    return
  fi

  if payload="$(download_text \
    "$(release_api_url "$api_base_url" "$repo")" 2>/dev/null)"; then
    tag="$(extract_latest_tag_from_json \
      "$payload" \
      "$DEFAULT_RELEASE_PREFIX")"
    if [ -n "$tag" ]; then
      printf '%s' "$(normalize_version "$tag")"
      return
    fi
  fi

  if payload="$(download_text \
    "$(release_feed_url "$download_base_url" "$repo")" 2>/dev/null)"; then
    tag="$(extract_latest_tag_from_atom \
      "$payload" \
      "$DEFAULT_RELEASE_PREFIX")"
    if [ -n "$tag" ]; then
      printf '%s' "$(normalize_version "$tag")"
      return
    fi
  fi

  die "no published OpenClaw releases found in ${repo}"
}

archive_name() {
  local version="$1"
  local goos="$2"
  local goarch="$3"

  printf '%s-%s-%s-%s.tar.gz' \
    "$PACKAGE_ROOT_NAME" "$version" "$goos" "$goarch"
}

asset_download_url() {
  local download_base_url="$1"
  local repo="$2"
  local version="$3"
  local asset="$4"
  local tag

  tag="$(release_tag "$version")"
  printf '%s/%s/releases/download/%s/%s' \
    "$(trim_trailing_slash "$download_base_url")" \
    "$repo" \
    "$tag" \
    "$asset"
}

verify_checksum() {
  local checksums_file="$1"
  local archive_path="$2"

  if command -v sha256sum >/dev/null 2>&1; then
    (
      cd "$(dirname "$archive_path")"
      sha256sum -c "$checksums_file" --ignore-missing >/dev/null
    )
    return
  fi

  if command -v shasum >/dev/null 2>&1; then
    local expected actual
    expected="$(awk \
      -v name="$(basename "$archive_path")" \
      '$2 == name { print $1 }' \
      "$checksums_file")"
    [ -n "$expected" ] || die "missing checksum for $(basename "$archive_path")"
    actual="$(shasum -a 256 "$archive_path" | awk '{ print $1 }')"
    [ "$expected" = "$actual" ] || \
      die "checksum mismatch for $(basename "$archive_path")"
    return
  fi

  warn "sha256sum/shasum not found; skip checksum validation"
}

print_path_hint() {
  local bin_dir="$1"
  local display_bin_dir rc_path display_rc_path export_line

  display_bin_dir="$(display_path "$bin_dir")"
  rc_path="$(shell_rc_path)"
  display_rc_path="$(display_path "$rc_path")"
  export_line="export PATH=\"${display_bin_dir}:\$PATH\""

  case ":${PATH}:" in
    *":${bin_dir}:"*)
      ;;
    *)
      log ""
      log "Add this directory to PATH before using ${BINARY_NAME}:"
      log "  ${export_line}"
      log ""
      log "Persist PATH for future shells:"
      log "  grep -qxF '${export_line}' \"${display_rc_path}\" || \\"
      log "    printf '\\n${export_line}\\n' >> \"${display_rc_path}\""
      log "  . \"${display_rc_path}\""
      ;;
  esac
}

display_path() {
  local path="$1"

  case "$path" in
    "${HOME}"/*)
      printf '$HOME%s' "${path#${HOME}}"
      ;;
    *)
      printf '%s' "$path"
      ;;
  esac
}

shell_rc_path() {
  case "$(basename "${SHELL:-bash}")" in
    zsh)
      printf '%s/.zshrc' "${HOME}"
      ;;
    *)
      printf '%s/.bashrc' "${HOME}"
      ;;
  esac
}

print_profile_hint() {
  local profile="$1"
  local config_dir="$2"
  local state_dir="$3"
  local display_config_dir display_state_dir

  display_config_dir="$(display_path "$config_dir")"
  display_state_dir="$(display_path "$state_dir")"

  log ""
  log "Profiles:"
  log "  ${display_config_dir}/${PROFILE_DIR_NAME}/openclaw.stdin.yaml"
  log "  ${display_config_dir}/${PROFILE_DIR_NAME}/openclaw.stdin.sqlite.yaml"
  log "  ${display_config_dir}/${PROFILE_DIR_NAME}/openclaw.telegram.yaml"
  log "Bundled skills:"
  log "  ${display_state_dir}/${BUNDLED_SKILLS_DIR_NAME}"
  log "Managed skills:"
  log "  ${display_state_dir}/${MANAGED_SKILLS_DIR_NAME}"

  case "$profile" in
    "$profileTelegram")
      log ""
      log "Telegram profile selected. Load credentials before starting:"
      log "  export TELEGRAM_BOT_TOKEN='replace-with-your-token'"
      log "  export OPENAI_API_KEY='replace-with-your-api-key'"
      log "  # optional:"
      log "  # export OPENAI_BASE_URL='https://your-endpoint/v1'"
      ;;
  esac
}

install_bundled_skills() {
  local source_dir="$1"
  local state_dir="$2"
  local target_dir tmp_dir

  target_dir="${state_dir}/${BUNDLED_SKILLS_DIR_NAME}"
  tmp_dir="${target_dir}.tmp"

  rm -rf "$tmp_dir"
  mkdir -p "$tmp_dir"
  cp -R "${source_dir}/." "$tmp_dir/"
  rm -rf "$target_dir"
  mv "$tmp_dir" "$target_dir"
}

write_install_metadata() {
  local bin_dir="$1"
  local config_dir="$2"
  local state_dir="$3"
  local target_file tmp_file

  target_file="${bin_dir}/${INSTALL_METADATA_FILE_NAME}"
  tmp_file="${target_file}.tmp"
  cat > "$tmp_file" <<EOF
bin_dir=${bin_dir}
config_dir=${config_dir}
state_dir=${state_dir}
EOF
  chmod 0644 "$tmp_file"
  mv "$tmp_file" "$target_file"
}

main() {
  local version=""
  local profile="$DEFAULT_PROFILE"
  local repo="$DEFAULT_RELEASE_REPO"
  local api_base_url="$DEFAULT_API_BASE_URL"
  local download_base_url="$DEFAULT_DOWNLOAD_BASE_URL"
  local force_config="false"
  local bin_dir="${HOME}/${DEFAULT_BIN_SUBDIR}"
  local config_dir="${HOME}/${DEFAULT_CONFIG_SUBDIR}"
  local state_dir="${HOME}/${DEFAULT_STATE_SUBDIR}"

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version)
        version="${2:-}"
        shift 2
        ;;
      --profile)
        profile="${2:-}"
        shift 2
        ;;
      --repo)
        repo="${2:-}"
        shift 2
        ;;
      --api-base-url)
        api_base_url="${2:-}"
        shift 2
        ;;
      --download-base-url)
        download_base_url="${2:-}"
        shift 2
        ;;
      --bin-dir)
        bin_dir="${2:-}"
        shift 2
        ;;
      --config-dir)
        config_dir="${2:-}"
        shift 2
        ;;
      --state-dir)
        state_dir="${2:-}"
        shift 2
        ;;
      --force-config)
        force_config="true"
        shift
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

  require_cmd curl
  require_cmd tar
  require_cmd install
  require_cmd grep
  require_cmd sed
  require_cmd awk

  local resolved_version goos goarch
  local archive_url checksums_url
  local temp_root unpack_dir archive_path checksums_path
  local archive selected_profile_path
  local package_root metadata_file
  local package_version sqlite_backend
  local display_bin_dir display_config_dir display_state_dir

  resolved_version="$(resolve_version \
    "$version" \
    "$repo" \
    "$api_base_url" \
    "$download_base_url")"
  [ -n "$resolved_version" ] || die "empty release version"

  goos="$(detect_os)"
  goarch="$(detect_arch)"
  archive="$(archive_name "$resolved_version" "$goos" "$goarch")"

  archive_url="$(asset_download_url \
    "$download_base_url" "$repo" "$resolved_version" "$archive")"
  checksums_url="$(asset_download_url \
    "$download_base_url" "$repo" "$resolved_version" \
    "$CHECKSUMS_FILE_NAME")"

  temp_root="$(mktemp -d)"
  unpack_dir="${temp_root}/unpack"
  archive_path="${temp_root}/${archive}"
  checksums_path="${temp_root}/${CHECKSUMS_FILE_NAME}"
  mkdir -p "$unpack_dir"

  download_file "$checksums_url" "$checksums_path"
  download_file "$archive_url" "$archive_path"
  verify_checksum "$checksums_path" "$archive_path"

  tar -xzf "$archive_path" -C "$unpack_dir"
  package_root="${unpack_dir}/${PACKAGE_ROOT_NAME}"
  [ -d "$package_root" ] || die "invalid package layout"

  mkdir -p "$bin_dir"
  mkdir -p "$config_dir/${PROFILE_DIR_NAME}"
  mkdir -p "$state_dir"
  bin_dir="$(resolve_dir_path "$bin_dir")"
  config_dir="$(resolve_dir_path "$config_dir")"
  state_dir="$(resolve_dir_path "$state_dir")"
  mkdir -p "$config_dir/${PROFILE_DIR_NAME}"

  install -m 0755 \
    "${package_root}/bin/${BINARY_NAME}" \
    "${bin_dir}/${BINARY_NAME}"
  install -m 0644 \
    "${package_root}/config/openclaw.stdin.yaml" \
    "${config_dir}/${PROFILE_DIR_NAME}/openclaw.stdin.yaml"
  install -m 0644 \
    "${package_root}/config/openclaw.stdin.sqlite.yaml" \
    "${config_dir}/${PROFILE_DIR_NAME}/openclaw.stdin.sqlite.yaml"
  install -m 0644 \
    "${package_root}/config/openclaw.telegram.yaml" \
    "${config_dir}/${PROFILE_DIR_NAME}/openclaw.telegram.yaml"

  selected_profile_path="${config_dir}/${PROFILE_DIR_NAME}/$(profile_file_name "$profile")"
  if [ "$force_config" = "true" ] || \
    [ ! -f "${config_dir}/openclaw.yaml" ]; then
    cp "$selected_profile_path" "${config_dir}/openclaw.yaml"
  fi

  install_bundled_skills "${package_root}/skills" "$state_dir"
  write_install_metadata "$bin_dir" "$config_dir" "$state_dir"

  metadata_file="${package_root}/metadata.env"
  package_version="$resolved_version"
  sqlite_backend="unknown"
  if [ -f "$metadata_file" ]; then
    # shellcheck disable=SC1090
    . "$metadata_file"
    package_version="${PACKAGE_VERSION:-$resolved_version}"
    sqlite_backend="${SQLITE_BACKEND:-unknown}"
  fi

  display_bin_dir="$(display_path "$bin_dir")"
  display_config_dir="$(display_path "$config_dir")"
  display_state_dir="$(display_path "$state_dir")"

  log ""
  log "openclaw installed."
  log "Package: ${package_version}"
  log "Binary: ${display_bin_dir}/${BINARY_NAME}"
  log "Profile: ${profile}"
  log "Config: ${display_config_dir}/openclaw.yaml"
  log "State:  ${display_state_dir}"
  log "SQLite: ${sqlite_backend}"
  log ""
  log "Run:"
  log "  ${display_bin_dir}/${BINARY_NAME}"

  print_profile_hint "$profile" "$config_dir" "$state_dir"
  print_path_hint "$bin_dir"

  rm -rf "$temp_root"
}

main "$@"
