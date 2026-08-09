#!/usr/bin/env bash
# Vagrant integration test for the task cgroup-isolation fix (#227).
#
# Boots the shared Vagrant guest, syncs the hostlink repo, builds it and runs
# the scope tests against the guest's real systemd:
#   - TestBuildTaskCmd_ScopeIsolation: task cgroup = own scope, exit codes
#   - TestScope_SurvivesParentDeath: task survives agent (parent) death
#   - TestScope_MemoryIsolation: 200M task alloc lands in scope, not caller
#   - TestUnitFile_*: shipped unit keeps sane MemoryHigh/MemoryMax
#   - systemd-analyze verify on the installed unit path
#
# Usage:
#   scripts/vagrant_scope_test.sh
# Env:
#   HOSTLINK_VAGRANT_GUEST - guest name (default: ubuntu1)
#   SELFHOST_REPO          - path to selfhost repo holding the Vagrantfile
#                            (default: ../selfhost)
set -euo pipefail

guest_name="${HOSTLINK_VAGRANT_GUEST:-ubuntu1}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
selfhost_repo="${SELFHOST_REPO:-${repo_root}/../selfhost}"

if [[ ! -f "${selfhost_repo}/Vagrantfile" ]]; then
  printf 'Vagrantfile not found at %s (set SELFHOST_REPO)\n' "${selfhost_repo}" >&2
  exit 1
fi

ssh_guest() { (cd "${selfhost_repo}" && vagrant ssh "${guest_name}" --command "export PATH=/usr/local/go/bin:\$PATH; $1"); }

echo "==> booting ${guest_name}"
(cd "${selfhost_repo}" && vagrant up "${guest_name}")

echo "==> syncing hostlink repo"
ssh_guest 'rm -rf /tmp/hostlink-scope-test && mkdir -p /tmp/hostlink-scope-test'
tar --no-xattrs -C "${repo_root}" --exclude .git --exclude .jj -czf - . | ssh_guest 'tar -xzf - -C /tmp/hostlink-scope-test'

echo "==> building"
ssh_guest 'cd /tmp/hostlink-scope-test && go build -o /tmp/hostlink-scope-test/hostlink . && sudo cp /tmp/hostlink-scope-test/hostlink /usr/bin/hostlink'

echo "==> running scope + unit tests (as root: polkit blocks user-created scopes)"
ssh_guest 'cd /tmp/hostlink-scope-test && sudo env "PATH=/usr/local/go/bin:$PATH" go test ./app/jobs/taskjob/ -run "Scope" -v && sudo env "PATH=/usr/local/go/bin:$PATH" go test ./scripts/linux/ -v'

echo "==> systemd-analyze verify (real unit path, real binary installed)"
ssh_guest 'sudo systemd-analyze verify /tmp/hostlink-scope-test/scripts/linux/hostlink.service && echo VERIFY_OK'

echo "==> PASS"
