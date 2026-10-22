
# Hostlink

It makes managing software on your machines very easy.

This project is trying to be an agent process which make managing software on the host machine very easy.

## Installation

Just like it, the installation is also very simple.

### Quick Install (with default credentials)

```sh
curl -fsSL https://raw.githubusercontent.com/selfhost-dev/hostlink/refs/heads/main/scripts/linux/install.sh | sudo bash
```

### Install with Custom Token

```sh
curl -fsSL https://raw.githubusercontent.com/selfhost-dev/hostlink/refs/heads/main/scripts/linux/install.sh | \
  sudo bash -s -- --token-id "your-token-id" --token-key "your-token-key"
```

### Install with Custom Server URL

```sh
curl -fsSL https://raw.githubusercontent.com/selfhost-dev/hostlink/refs/heads/main/scripts/linux/install.sh | \
  sudo bash -s -- --server-url "https://your-server.com" --token-id "your-token-id" --token-key "your-token-key"
```

### Install outcome: exit codes and status file

The installer exits non-zero when any step fails, so a caller that retries the
install (cloud-init user data, selfhost's `Hostlink::InstallScript`) retries
instead of reading a half-done install as success:

| Exit | Meaning |
|---|---|
| 0 | Installed, and the `hostlink` service is running (verified: active within 20s and still active 3s later) |
| 1 | General error (not root, unknown option) |
| 2 | Missing dependency (curl, or a gzip check tool: xxd, od or file) |
| 3 | The release version could not be determined (GitHub API, after retries) |
| 4 | The release could not be downloaded (after retries, checksum verified) |
| 5 | Unsupported architecture |
| 6 | An install step failed (extract, move, directories, env, service) |
| 7 | The service did not come up, or stopped right after starting |

Every run writes `/var/lib/hostlink/install-status` (key=value lines: `status`
`running`/`installed`/`failed`, `stage`, `version`, `exit_code`, `updated_at`)
for recovery tooling. It never holds the token.

### Install a pinned version, from a mirror, with checksums

By default the installer asks GitHub's API for the latest release (unauthenticated,
rate-limited per IP) and downloads it from GitHub. To skip both:

```sh
curl -fsSL https://<bucket>.s3.amazonaws.com/hostlink/v0.8.45/install.sh -o install.sh
sudo bash install.sh --server-url "https://your-server.com" --token-id "your-token-id" --token-key "your-token-key" \
  --version v0.8.45 \
  --download-base-url https://<bucket>.s3.amazonaws.com/hostlink/v0.8.45 \
  --sha256-x86_64 <sha256> --sha256-arm64 <sha256>
```

- `--version` installs that tag and never calls GitHub's API.
- `--download-base-url` downloads `hostlink_Linux_<arch>.tar.gz` from there instead of the GitHub release.
- `--sha256-x86_64` / `--sha256-arm64`: a download whose sha256 does not match the one for the machine's arch is rejected and retried.

Every tagged release is mirrored to S3 by the release workflow
(`scripts/release/mirror_to_s3.sh`) once the repository variables
`HOSTLINK_MIRROR_BUCKET` and `HOSTLINK_MIRROR_ROLE_ARN` are set.

Just running this above script will make the server up and running on your
machine. You can access it on port :1232 of your machine.

### Configuration

After installation, you can modify the configuration at `/etc/hostlink/hostlink.env`:

```bash
SH_CONTROL_PLANE_URL=http://localhost:8080
HOSTLINK_TOKEN_ID=your-token-id
HOSTLINK_TOKEN_KEY=your-token-key
```

After modifying the configuration, restart the service:

```sh
sudo systemctl restart hostlink
```

## Authentication Architecture

Hostlink uses **one-way authentication** for secure agent-to-server communication:

### Agent → Server Authentication

- **Agent signs requests** using RSA-PSS signatures with SHA-256
- **Server verifies signatures** using agent's public key stored in database
- **Timestamp-based replay protection** with 5-minute window (±300 seconds)
- **No nonce storage** required - stateless verification for horizontal scalability

### Server → Agent Communication

- **Server does NOT sign responses** - authentication is one-way only
- **HTTPS/TLS provides transport security** between server and agent

### Authentication Flow

1. **Agent Registration**
   - Agent generates RSA key pair during installation
   - Sends public key to server during registration
   - Server stores public key in database associated with Agent ID

2. **Authenticated Requests**
   - Agent creates message: `AgentID|Timestamp|Nonce`
   - Signs message with private key using RSA-PSS
   - Sends request with headers: `X-Agent-ID`, `X-Timestamp`, `X-Nonce`, `X-Signature`

3. **Server Verification**
   - Retrieves agent's public key from database
   - Verifies timestamp is within 5-minute window
   - Reconstructs message and verifies signature
   - Returns 401 Unauthorized if verification fails

### Security Features

- ✅ Cryptographic agent authentication
- ✅ Replay attack prevention via timestamp validation
- ✅ Stateless verification (no server-side nonce tracking)
- ✅ Horizontally scalable authentication
- ✅ HTTPS/TLS for transport security

## hlctl - CLI Tool

`hlctl` is the command-line interface for managing Hostlink tasks and agents.

### Quick Start

**Build from source:**

```bash
go build -o hlctl cmd/hlctl/main.go
```

**Configure server URL:**

```bash
# Via environment variable
export HOSTLINK_SERVER_URL=http://localhost:8080

# Or via config file
mkdir -p ~/.hostlink
echo "server: http://localhost:8080" > ~/.hostlink/config.yml
```

**Create and execute a task:**

```bash
# Create a task
hlctl task create --command "echo 'Hello World'"

# List tasks
hlctl task list

# Get task details
hlctl task get <task-id>
```

**Manage agents:**

```bash
# List all agents
hlctl agent list

# Get agent details
hlctl agent get <agent-id>
```

For complete documentation, see [docs/hlctl.md](docs/hlctl.md).

## Upcoming Features

- Agent self update
- Passing parameter in script
- Following a workflow from the remote control plane
- Following multiple workflow from the remote control plane
- Adding support for migrations in workflow
- Adding support to revert migrations for any issue
- Registering multiple agents to the same workflow
- Task scheduling for any future date
- CRUD on the future task from the control plane
