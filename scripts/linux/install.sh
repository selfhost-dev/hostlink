#!/usr/bin/env bash

# Exit codes
EXIT_GENERAL=1
EXIT_DEPENDENCY=2
EXIT_VERSION_FETCH=3
EXIT_DOWNLOAD=4
EXIT_UNSUPPORTED_ARCH=5
EXIT_INSTALL=6
EXIT_SERVICE=7

# Retry configuration
MAX_RETRIES=5
INITIAL_DELAY=1
MAX_DELAY=60
DOWNLOAD_TIMEOUT=60

# After start, the service must be active within VERIFY_TIMEOUT seconds and
# still active VERIFY_SETTLE seconds later (a crash loop is briefly active).
VERIFY_TIMEOUT=20
VERIFY_SETTLE=3

# The install's outcome, for recovery tooling (hostlink#236): key=value lines
# status (running|installed|unverified|failed), stage, version, exit_code, updated_at.
# Under /var/lib/hostlink, not cloud-init's directory: servers installed over
# SSH (Coolify) have no cloud-init. Never holds the token.
INSTALL_STATUS_FILE=/var/lib/hostlink/install-status
STAGE="start"


check_dependencies() {
  # Check for curl
  if ! command -v curl &> /dev/null; then
    echo "ERROR: curl is required but not installed."
    echo "Please install curl and try again."
    exit $EXIT_DEPENDENCY
  fi

  # Check for gzip validation tool (in priority order)
  if command -v xxd &> /dev/null; then
    GZIP_VALIDATOR="xxd"
  elif command -v od &> /dev/null; then
    GZIP_VALIDATOR="od"
  elif command -v file &> /dev/null; then
    GZIP_VALIDATOR="file"
  else
    echo "ERROR: No gzip validation tool found."
    echo "Please install one of: xxd, od, or file"
    exit $EXIT_DEPENDENCY
  fi
}

create_temp_dir() {
  TEMP_DIR=$(mktemp -d)
  if [ ! -d "$TEMP_DIR" ]; then
    echo "ERROR: Failed to create temporary directory."
    exit $EXIT_GENERAL
  fi
  echo "Using temporary directory: $TEMP_DIR"
}

validate_gzip() {
  local file="$1"
  
  if [ ! -f "$file" ]; then
    return 1
  fi

  case $GZIP_VALIDATOR in
    xxd)
      local magic=$(xxd -p -l 2 "$file" 2>/dev/null)
      [ "$magic" = "1f8b" ]
      ;;
    od)
      local magic=$(od -A n -t x1 -N 2 "$file" 2>/dev/null | tr -d ' \n')
      [ "$magic" = "1f8b" ]
      ;;
    file)
      file "$file" 2>/dev/null | grep -q gzip
      ;;
    *)
      return 1
      ;;
  esac
}

calculate_backoff() {
  local attempt=$1
  local delay=$((INITIAL_DELAY << (attempt - 1)))
  if [ "$delay" -gt "$MAX_DELAY" ]; then
    delay=$MAX_DELAY
  fi
  # Add jitter (0-1 second)
  local jitter
  jitter=$(awk 'BEGIN{srand(); printf "%.2f", rand()}')
  awk "BEGIN{printf \"%.2f\", $delay + $jitter}"
}

show_download_error() {
  local url="$1"
  local http_status="$2"
  local file="$3"
  local file_size=0
  local file_content=""

  if [ -f "$file" ]; then
    file_size=$(stat -c%s "$file" 2>/dev/null || stat -f%z "$file" 2>/dev/null || echo "unknown")
    file_content=$(head -c 100 "$file" 2>/dev/null | tr -cd '[:print:]')
  fi

  echo ""
  echo "ERROR: Failed to download hostlink after $MAX_RETRIES attempts."
  echo ""
  echo "Last attempt details:"
  echo "  - HTTP Status: $http_status"
  echo "  - File size received: $file_size bytes"
  if [ -n "$file_content" ]; then
    echo "  - File content (first 100 chars): \"$file_content\""
  fi
  echo "  - Expected: Valid gzip archive (>1MB typically)"
  echo ""
  echo "Manual download URL:"
  echo "  $url"
  echo ""
  echo "You can download this file manually and extract it to /usr/bin/hostlink"
  echo "Downloaded file location: $file"
}

# Default values
TOKEN_ID="default-token-id"
TOKEN_KEY="default-token-key"
SERVER_URL="http://localhost:8080"
# Pinned release tag (e.g. v0.8.45). Empty: ask GitHub's API for the latest,
# which is unauthenticated and rate-limited per IP (60/h) — a pinned version
# skips that lookup entirely.
VERSION=""
# Where hostlink_Linux_<arch>.tar.gz is downloaded from. Empty: this version's
# GitHub release. A mirror (e.g. https://<bucket>.s3.amazonaws.com/hostlink/v0.8.45)
# keeps installs off GitHub.
DOWNLOAD_BASE_URL=""
# Expected sha256 of the tarball for each architecture. When the one for this
# machine's arch is set, a download that does not match is rejected and retried.
SHA256_X86_64=""
SHA256_ARM64=""
# The agent's HTTP port (SH_APP_PORT), written before its first start. Empty:
# the agent's default, 8080, which Coolify's proxy already holds.
APP_PORT=""
HOSTLINK_ENV_FILE=/etc/hostlink/hostlink.env
# Set when the agent's port is taken before start (see check_port_free).
SKIP_VERIFY=0

usage() {
  echo "Usage: $0 [--token-id ID] [--token-key KEY] [--server-url URL] [--version TAG]"
  echo "          [--download-base-url URL] [--sha256-x86_64 HEX] [--sha256-arm64 HEX] [--app-port PORT]"
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case $1 in
      --token-id)
        TOKEN_ID="$2"
        shift 2
        ;;
      --token-key)
        TOKEN_KEY="$2"
        shift 2
        ;;
      --server-url)
        SERVER_URL="$2"
        shift 2
        ;;
      --version)
        VERSION="$2"
        shift 2
        ;;
      --download-base-url)
        DOWNLOAD_BASE_URL="$2"
        shift 2
        ;;
      --sha256-x86_64)
        SHA256_X86_64="$2"
        shift 2
        ;;
      --sha256-arm64)
        SHA256_ARM64="$2"
        shift 2
        ;;
      --app-port)
        APP_PORT="$2"
        shift 2
        ;;
      *)
        echo "Unknown option: $1"
        usage
        exit 1
        ;;
    esac
  done
}

uninstall_existing() {
  echo "Checking for existing installation..."
  if systemctl is-active --quiet hostlink; then
    echo "Stopping existing hostlink service..."
    sudo systemctl stop hostlink
  fi

  if systemctl is-enabled --quiet hostlink 2>/dev/null; then
    echo "Disabling existing hostlink service..."
    sudo systemctl disable hostlink
  fi

  if [ -f /etc/systemd/system/hostlink.service ]; then
    echo "Removing old service file..."
    sudo rm /etc/systemd/system/hostlink.service
    sudo systemctl daemon-reload
  fi

  if [ -f /usr/bin/hostlink ]; then
    echo "Removing old binary..."
    sudo rm /usr/bin/hostlink
  fi

  echo "Cleanup complete."
}

latest_version() {
  local api_url="https://api.github.com/repos/selfhost-dev/hostlink/releases/latest"
  local attempt=0
  local version=""
  local http_status=""
  local temp_file="$TEMP_DIR/version_response.json"

  echo "Fetching latest version..." >&2

  while [ "$attempt" -lt "$MAX_RETRIES" ]; do
    attempt=$((attempt + 1))

    # Fetch with timeout and capture HTTP status
    http_status=$(curl -sS --max-time "$DOWNLOAD_TIMEOUT" -w '%{http_code}' -o "$temp_file" "$api_url" 2>/dev/null)
    
    # Check for 404 - fail immediately
    if [ "$http_status" = "404" ]; then
      echo "ERROR: Could not find release information (HTTP 404)." >&2
      echo "The GitHub API endpoint may have changed or the repository may not exist." >&2
      exit $EXIT_VERSION_FETCH
    fi

    # Check for success (2xx status)
    if [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
      version=$(grep tag_name "$temp_file" 2>/dev/null | cut -d'"' -f4)
      
      if [ -n "$version" ]; then
        echo "Latest version: $version" >&2
        rm -f "$temp_file"
        echo "$version"
        return 0
      fi
    fi

    # If we get here, we need to retry (unless it's the last attempt)
    if [ $attempt -lt $MAX_RETRIES ]; then
      local delay
      delay=$(calculate_backoff $attempt)
      echo "Version fetch failed (HTTP $http_status). Retry $attempt/$MAX_RETRIES in ${delay}s..." >&2
      sleep "$delay"
    fi
  done

  # All retries exhausted
  echo "" >&2
  echo "ERROR: Failed to fetch latest version after $MAX_RETRIES attempts." >&2
  echo "Last HTTP status: $http_status" >&2
  if [ -f "$temp_file" ]; then
    local content
    content=$(head -c 100 "$temp_file" 2>/dev/null | tr -cd '[:print:]')
    if [ -n "$content" ]; then
      echo "Response content: \"$content\"" >&2
    fi
  fi
  echo "" >&2
  echo "Please check your internet connection and try again." >&2
  rm -f "$temp_file"
  exit $EXIT_VERSION_FETCH
}

# The release tag to install: the pinned --version, else GitHub's latest.
resolve_version() {
  if [ -n "$VERSION" ]; then
    echo "Using pinned version: $VERSION" >&2
    echo "$VERSION"
  else
    latest_version
  fi
}

download_url() {
  local base="${DOWNLOAD_BASE_URL%/}"
  if [ -z "$base" ]; then
    base="https://github.com/selfhost-dev/hostlink/releases/download/${VERSION}"
  fi
  echo "${base}/hostlink_Linux_${ARCH}.tar.gz"
}

expected_sha256() {
  case $ARCH in
    x86_64) echo "$SHA256_X86_64" ;;
    arm64) echo "$SHA256_ARM64" ;;
  esac
}

# True when no checksum is expected for this arch, or the file matches it.
verify_sha256() {
  local file="$1"
  local expected
  expected=$(expected_sha256)
  [ -z "$expected" ] && return 0

  if ! command -v sha256sum &> /dev/null; then
    echo "ERROR: sha256sum is required to verify the download." >&2
    return 1
  fi
  local actual
  actual=$(sha256sum "$file" | cut -d' ' -f1)
  [ "$actual" = "$expected" ]
}

detect_arch() {
  local arch=$(uname -m)
  case $arch in
    x86_64)
      echo "x86_64"
      ;;
    aarch64|arm64)
      echo "arm64"
      ;;
    *)
      echo "ERROR: Unsupported architecture: $arch" >&2
      exit $EXIT_UNSUPPORTED_ARCH
      ;;
  esac
}


download_tar() {
  local download_url
  download_url=$(download_url)
  local tar_file="$TEMP_DIR/$HOSTLINK_TAR"
  local attempt=0
  local http_status=""

  echo "Downloading hostlink $VERSION from $download_url..."

  while [ "$attempt" -lt "$MAX_RETRIES" ]; do
    attempt=$((attempt + 1))

    # Download with timeout and capture HTTP status
    http_status=$(curl -L -sS --max-time "$DOWNLOAD_TIMEOUT" -w '%{http_code}' -o "$tar_file" "$download_url" 2>/dev/null)

    # Check for 404 - fail immediately
    if [ "$http_status" = "404" ]; then
      echo ""
      echo "ERROR: Release not found (HTTP 404)."
      echo "Version $VERSION may not exist or the release assets may not be available."
      echo ""
      echo "Manual download URL:"
      echo "  $download_url"
      exit $EXIT_DOWNLOAD
    fi

    # Check for success (2xx status) and validate gzip
    if [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
      if ! validate_gzip "$tar_file"; then
        echo "Download completed but file is not valid gzip (possibly corrupted)."
      elif ! verify_sha256 "$tar_file"; then
        echo "Download completed but its sha256 does not match the expected checksum for ${ARCH}."
      else
        echo "Download successful."
        return 0
      fi
    fi

    # If we get here, we need to retry (unless it's the last attempt)
    if [ "$attempt" -lt "$MAX_RETRIES" ]; then
      local delay
      delay=$(calculate_backoff "$attempt")
      if [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
        echo "Validation failed. Retry $attempt/$MAX_RETRIES in ${delay}s..."
      else
        echo "Download failed (HTTP $http_status). Retry $attempt/$MAX_RETRIES in ${delay}s..."
      fi
      sleep "$delay"
    fi
  done

  # All retries exhausted
  show_download_error "$download_url" "$http_status" "$tar_file"
  exit $EXIT_DOWNLOAD
}

extract_tar() {
  echo "Extracting archive..."
  tar -xvf "$TEMP_DIR/$HOSTLINK_TAR" -C "$TEMP_DIR"
}

move_bin() {
  echo "Moving binary to /usr/bin..."
  sudo mv "$TEMP_DIR/hostlink" /usr/bin/hostlink
}

create_directories() {
  echo "Creating hostlink directories..."
  sudo mkdir -p /var/lib/hostlink &&
    sudo mkdir -p /var/log/hostlink &&
    sudo mkdir -p /etc/hostlink &&
    sudo chmod 700 /var/lib/hostlink &&
    sudo chmod 755 /var/log/hostlink &&
    sudo chmod 755 /etc/hostlink || return 1
  echo "Directories created."
}

create_env_file() {
  echo "Creating environment configuration..."
  cat > "$HOSTLINK_ENV_FILE" <<EOF || return 1
SH_CONTROL_PLANE_URL=$SERVER_URL
HOSTLINK_TOKEN_ID=$TOKEN_ID
HOSTLINK_TOKEN_KEY=$TOKEN_KEY
EOF
  if [ -n "$APP_PORT" ]; then
    echo "SH_APP_PORT=$APP_PORT" >> "$HOSTLINK_ENV_FILE" || return 1
  fi
  sudo chmod 600 "$HOSTLINK_ENV_FILE" || return 1
  echo "Environment file created at $HOSTLINK_ENV_FILE"
}

install_service() {
  echo "Installing systemd service..."
  sudo cp "$TEMP_DIR/scripts/hostlink.service" /etc/systemd/system/ &&
    sudo systemctl daemon-reload &&
    sudo systemctl enable hostlink &&
    sudo systemctl start hostlink || return 1
  echo "Service started."
}

# Before start (the old agent already stopped): when another process holds
# the agent's port, the agent cannot start until the caller moves it. That is
# a Coolify server installed without --app-port: Traefik holds 8080, and the
# caller appends SH_APP_PORT and restarts the agent after this installer.
# Install, but do not verify: the check would fail every such server.
check_port_free() {
  local port="${APP_PORT:-8080}"
  # No ss: whether the port is free cannot be told, and a false failure is
  # worse than no check (the old behaviour).
  if ! command -v ss &> /dev/null; then
    echo "WARNING: ss not found; cannot tell whether port $port is free. Skipping the service check." >&2
    SKIP_VERIFY=1
    return 0
  fi
  if [ -n "$(ss -Hltn "sport = :$port" 2>/dev/null)" ]; then
    echo "WARNING: port $port is already in use; the agent cannot start on it until it is moved (--app-port). Skipping the service check." >&2
    ss -Hltnp "sport = :$port" >&2 2>/dev/null || true
    SKIP_VERIFY=1
  fi
}

# The service must come up, and stay up past VERIFY_SETTLE: an agent that
# crashes on start is active for a moment before systemd restarts it.
verify_service() {
  echo "Verifying the hostlink service..."
  local waited=0
  until systemctl is-active --quiet hostlink; do
    if [ "$waited" -ge "$VERIFY_TIMEOUT" ]; then
      echo "ERROR: hostlink service is not active after ${VERIFY_TIMEOUT}s." >&2
      systemctl status hostlink --no-pager -n 20 >&2 || true
      exit $EXIT_SERVICE
    fi
    sleep 1
    waited=$((waited + 1))
  done
  sleep "$VERIFY_SETTLE"
  if ! systemctl is-active --quiet hostlink; then
    echo "ERROR: hostlink service stopped right after starting." >&2
    systemctl status hostlink --no-pager -n 20 >&2 || true
    exit $EXIT_SERVICE
  fi
  echo "hostlink service is running."
}

# Each step returns non-zero when any of its commands fails, not only its
# last one; run_step turns that into a non-zero exit naming the step, so a
# caller retrying the install (selfhost's Hostlink::InstallScript) retries
# instead of reading a half-done install as success (hostlink#236).
run_step() {
  STAGE="$1"
  shift
  if ! "$@"; then
    echo "ERROR: install step '$STAGE' failed." >&2
    exit $EXIT_INSTALL
  fi
}

write_install_status() {
  local status="$1" exit_code="$2" tmp
  mkdir -p "$(dirname "$INSTALL_STATUS_FILE")" 2>/dev/null || return 0
  tmp="$INSTALL_STATUS_FILE.tmp"
  {
    echo "status=$status"
    echo "stage=$STAGE"
    echo "version=${VERSION:-}"
    echo "exit_code=$exit_code"
    echo "updated_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } > "$tmp" 2>/dev/null && mv "$tmp" "$INSTALL_STATUS_FILE" 2>/dev/null
  return 0
}

# EXIT trap: every way out of main, an explicit exit included, records the
# outcome. Only a verified install is "installed".
finish_install() {
  local code="$1"
  if [ "$code" -eq 0 ] && [ "$STAGE" = "verify" ]; then
    if [ "$SKIP_VERIFY" = 1 ]; then
      write_install_status unverified 0
    else
      write_install_status installed 0
    fi
  else
    write_install_status failed "$code"
    echo "hostlink install failed at stage '$STAGE' (exit $code). Status: $INSTALL_STATUS_FILE" >&2
  fi
}

check_root() {
  if [[ $EUID -ne 0 ]]; then
    echo "This script must be run as root (use sudo)"
    exit $EXIT_GENERAL
  fi
}

main() {
  parse_args "$@"

  check_root

  trap 'finish_install $?' EXIT
  write_install_status running 0

  # Run dependency checks first
  STAGE="dependencies"
  check_dependencies
  create_temp_dir

  STAGE="arch"
  ARCH=$(detect_arch) || exit $EXIT_UNSUPPORTED_ARCH

  # resolve_version outputs messages to stderr, version to stdout
  STAGE="version"
  VERSION=$(resolve_version)
  if [ -z "$VERSION" ]; then
    echo "ERROR: Failed to determine version."
    exit $EXIT_VERSION_FETCH
  fi

  HOSTLINK_TAR=hostlink_$VERSION.tar.gz

  STAGE="uninstall"
  uninstall_existing
  STAGE="download"
  download_tar
  run_step extract extract_tar
  run_step move move_bin
  run_step directories create_directories
  run_step env create_env_file
  check_port_free
  run_step service install_service
  STAGE="verify"
  if [ "$SKIP_VERIFY" = 1 ]; then
    echo "hostlink service started; not verified (port in use)."
  else
    verify_service
  fi
}

# Run when executed — as a file or piped (`curl … | sudo bash -s -- …`) —
# never when sourced, so the functions can be tested (install_test.sh).
if ! (return 0 2>/dev/null); then
  main "$@"
fi
