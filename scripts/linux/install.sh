#!/usr/bin/env bash

# Exit codes
EXIT_GENERAL=1
EXIT_DEPENDENCY=2
EXIT_VERSION_FETCH=3
EXIT_DOWNLOAD=4
EXIT_UNSUPPORTED_ARCH=5

# Retry configuration
MAX_RETRIES=5
INITIAL_DELAY=1
MAX_DELAY=60
DOWNLOAD_TIMEOUT=60

# Check if running as root
if [[ $EUID -ne 0 ]]; then
  echo "This script must be run as root (use sudo)"
  exit $EXIT_GENERAL
fi

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

# Parse command line arguments
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
    *)
      echo "Unknown option: $1"
      echo "Usage: $0 [--token-id ID] [--token-key KEY] [--server-url URL]"
      exit 1
      ;;
  esac
done

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

# Run dependency checks first
check_dependencies
create_temp_dir

ARCH=$(detect_arch)

# latest_version outputs messages to stderr, version to stdout
VERSION=$(latest_version)
if [ -z "$VERSION" ]; then
  echo "ERROR: Failed to determine version."
  exit $EXIT_VERSION_FETCH
fi

HOSTLINK_TAR=hostlink_$VERSION.tar.gz

download_tar() {
  local download_url="https://github.com/selfhost-dev/hostlink/releases/download/${VERSION}/hostlink_Linux_${ARCH}.tar.gz"
  local tar_file="$TEMP_DIR/$HOSTLINK_TAR"
  local attempt=0
  local http_status=""

  echo "Downloading hostlink $VERSION..."

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
      if validate_gzip "$tar_file"; then
        echo "Download successful."
        return 0
      else
        echo "Download completed but file is not valid gzip (possibly corrupted)."
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
  sudo mkdir -p /var/lib/hostlink
  sudo mkdir -p /var/log/hostlink
  sudo mkdir -p /etc/hostlink
  sudo chmod 700 /var/lib/hostlink
  sudo chmod 755 /var/log/hostlink
  sudo chmod 755 /etc/hostlink
  echo "Directories created."
}

create_env_file() {
  echo "Creating environment configuration..."
  cat > /etc/hostlink/hostlink.env <<EOF
SH_CONTROL_PLANE_URL=$SERVER_URL
HOSTLINK_TOKEN_ID=$TOKEN_ID
HOSTLINK_TOKEN_KEY=$TOKEN_KEY
EOF
  sudo chmod 600 /etc/hostlink/hostlink.env
  echo "Environment file created at /etc/hostlink/hostlink.env"
}

install_service() {
  echo "Installing systemd service..."
  sudo cp "$TEMP_DIR/scripts/hostlink.service" /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable hostlink
  sudo systemctl start hostlink
  echo "Service started."
}

uninstall_existing
download_tar
extract_tar
move_bin
create_directories
create_env_file
install_service
