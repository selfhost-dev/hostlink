# hlctl - Hostlink CLI

Command-line interface for managing Hostlink tasks and agents.

## Overview

`hlctl` provides a simple way to interact with the Hostlink control plane from the command line. You can create tasks, monitor their execution, and manage agents without needing to make direct API calls.

**Key Features:**
- Create and manage tasks
- Monitor task execution and output
- List and inspect agents
- JSON output for easy parsing
- Configuration via file or environment variables

## Installation

### Build from Source

```bash
go build -o hlctl cmd/hlctl/main.go
```

### Install to $GOPATH/bin

```bash
go install ./cmd/hlctl
```

### Verify Installation

```bash
hlctl --version
```

## Configuration

`hlctl` needs to know the URL of your Hostlink server. You can configure this in three ways (in order of precedence):

### 1. Command-line Flag

```bash
hlctl --server http://localhost:8080 task list
```

### 2. Environment Variable

```bash
export HOSTLINK_SERVER_URL=http://localhost:8080
hlctl task list
```

### 3. Configuration File

Create `~/.hostlink/config.yml`:

```yaml
server: http://localhost:8080
```

Then run:

```bash
hlctl task list
```

**Default:** If no configuration is provided, `hlctl` defaults to `http://localhost:8080`.

## Task Management

### Create Tasks

Create a new task to be executed by agents.

**Basic usage:**

```bash
hlctl task create --command "echo 'Hello World'"
```

**Create task from a script file:**

```bash
hlctl task create --file /path/to/script.sh
```

**Set task priority (1-10, higher = more important):**

```bash
hlctl task create --command "systemctl restart nginx" --priority 5
```

**Target specific agents by tags:**

```bash
hlctl task create --command "df -h" --tag env=prod
hlctl task create --command "uptime" --tag env=prod --tag region=us-east
```

**Flags:**
- `--command` - Command to execute (mutually exclusive with `--file`)
- `--file` - Path to script file (mutually exclusive with `--command`)
- `--priority` - Task priority (1-10, default: 1)
- `--tag` - Filter agents by tag (format: `key=value`, repeatable)

**Example output:**

```json
{
  "id": "tsk_01HN6X8ZMJQK3P2V9Y0TXQR8WF",
  "status": "pending",
  "created_at": "2025-10-04T10:30:00Z"
}
```

### List Tasks

List all tasks with optional filtering.

**Basic usage:**

```bash
hlctl task list
```

**Filter by status:**

```bash
hlctl task list --status pending
hlctl task list --status completed
hlctl task list --status failed
```

**Filter by priority:**

```bash
hlctl task list --priority 5
```

**Combine filters:**

```bash
hlctl task list --status completed --priority 3
```

**Flags:**
- `--status` - Filter by status (pending, running, completed, failed)
- `--priority` - Filter by priority (1-10)

**Example output:**

```json
[
  {
    "id": "tsk_01HN6X8ZMJQK3P2V9Y0TXQR8WF",
    "command": "echo 'Hello World'",
    "status": "completed",
    "priority": 1,
    "created_at": "2025-10-04T10:30:00Z"
  }
]
```

### Get Task Details

Get detailed information about a specific task, including output.

**Basic usage:**

```bash
hlctl task get tsk_01HN6X8ZMJQK3P2V9Y0TXQR8WF
```

**Example output:**

```json
{
  "id": "tsk_01HN6X8ZMJQK3P2V9Y0TXQR8WF",
  "command": "echo 'Hello World'",
  "status": "completed",
  "priority": 1,
  "output": "Hello World\n",
  "exit_code": 0,
  "created_at": "2025-10-04T10:30:00Z",
  "started_at": "2025-10-04T10:30:05Z",
  "completed_at": "2025-10-04T10:30:06Z"
}
```

## Agent Management

### List Agents

List all registered agents.

**Basic usage:**

```bash
hlctl agent list
```

**Example output:**

```json
[
  {
    "id": "agt_01HN6X8ZMJQK3P2V9Y0TXQR8WF",
    "status": "active",
    "last_seen": "2025-10-04T10:30:00Z",
    "tags": [
      {"key": "env", "value": "prod"},
      {"key": "region", "value": "us-east-1"}
    ],
    "registered_at": "2025-10-01T00:00:00Z"
  }
]
```

### Get Agent Details

Get detailed information about a specific agent.

**Basic usage:**

```bash
hlctl agent get agt_01HN6X8ZMJQK3P2V9Y0TXQR8WF
```

**Example output:**

```json
{
  "id": "agt_01HN6X8ZMJQK3P2V9Y0TXQR8WF",
  "status": "active",
  "last_seen": "2025-10-04T10:30:00Z",
  "tags": [
    {"key": "env", "value": "prod"},
    {"key": "region", "value": "us-east-1"}
  ],
  "registered_at": "2025-10-01T00:00:00Z"
}
```

## Common Workflows

### Execute a Task and Monitor Results

```bash
# Create a task
TASK_ID=$(hlctl task create --command "df -h" | jq -r '.id')

# Wait a few seconds for execution
sleep 5

# Get task output
hlctl task get $TASK_ID
```

### Target Specific Environments

```bash
# Create task for production servers only
hlctl task create --command "systemctl status nginx" --tag env=prod

# Create task for specific region
hlctl task create --command "uptime" --tag region=us-west
```

### Execute Script from File

```bash
# Create deployment script
cat > deploy.sh <<'EOF'
#!/bin/bash
set -e
cd /var/www/myapp
git pull origin main
npm install
systemctl restart myapp
EOF

# Execute on all agents
hlctl task create --file deploy.sh --priority 10
```

### Monitor All Running Tasks

```bash
watch -n 2 'hlctl task list --status running'
```

## Troubleshooting

### Connection Refused

**Error:**
```
Error: Failed to create task: connection refused
```

**Solution:**
- Verify server is running
- Check server URL configuration
- Ensure firewall allows connection

### Invalid Command Syntax

**Error:**
```
Error: Invalid command syntax
```

**Solution:**
- Commands must be valid shell syntax
- Quote commands with special characters
- Use `--file` for complex scripts

### No Agents Available

If tasks remain in `pending` status:
- Verify agents are registered: `hlctl agent list`
- Check agent status in output
- Review agent logs for connection issues

### Configuration Not Found

**Error:**
```
Using default server: http://localhost:8080
```

**Solution:**
This is just a warning. To suppress it, configure the server URL using one of the methods in the [Configuration](#configuration) section.

## Global Options

All commands support these global options:

- `--server` - Override configured server URL
- `--help, -h` - Show help for command
- `--version, -v` - Show hlctl version

## Exit Codes

- `0` - Success
- `1` - General error
- `2` - Invalid arguments
