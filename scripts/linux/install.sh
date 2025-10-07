#!/usr/bin/env bash

# Check if running as root
if [[ $EUID -ne 0 ]]; then
  echo "This script must be run as root (use sudo)"
  exit 1
fi

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
  local version=$(curl -s https://api.github.com/repos/selfhost-dev/hostlink/releases/latest | grep tag_name | cut -d'"' -f4)
  echo $version
}

VERSION=$(latest_version)
HOSTLINK_TAR=hostlink_$VERSION.tar.gz

download_tar() {
  curl -L -o $HOSTLINK_TAR \
    https://github.com/selfhost-dev/hostlink/releases/download/${VERSION}/hostlink_Linux_x86_64.tar.gz
}

extract_tar() {
  tar -xvf $HOSTLINK_TAR
}

move_bin() {
  echo "Moving binary to /usr/bin, password prompt might be required."
  sudo mv ./hostlink /usr/bin/hostlink
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
  sudo cp ./scripts/hostlink.service /etc/systemd/system/
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
