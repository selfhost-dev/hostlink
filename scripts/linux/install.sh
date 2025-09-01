#!/bin/bash

# We are gonna do the installation/update of the agent with this file.
# So any kind of script is written should be written to keep in mind that this
# script in running state should do the both of the tasks

# I guess the starting will happen with first check what is the latest version
# of the agent or whether the version we got in the request is available itself
# or not. If the version isn't available or already latest version is installed
# then we can choose for a no-op. Do we need to notify this to the user it
# doesn't seem necessary at this moment but we can include certainly as we see
# any need for it.

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

install_service() {
  echo "Installing systemd service..."
  sudo cp ./scripts/hostlink.service /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable hostlink
  sudo systemctl start hostlink
  echo "Service started."
}

download_tar
extract_tar
move_bin
install_service
