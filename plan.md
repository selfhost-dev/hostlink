# hlctl MVP Implementation Plan

## Testing Strategy

- **50% Integration Tests**: End-to-end flows testing CLI → API → Agent → Response
- **30% Smoke Tests**: Basic functionality tests against running server (golang tests with `//go:build smoke` tag)
- **20% Unit Tests**: Individual function/component tests

## File Structure Guidelines

- **Controllers**: Add methods to existing files (e.g., `agents.go`, `tasks.go`) - don't create new files like `get.go`, `list.go`
- **Tests**: Add tests to existing `*_test.go` files - don't create separate test files per method
- **Smoke Tests**: Create golang tests in `test/smoke/` with `//go:build smoke` tag
  - Run with: `go test -tags=smoke ./test/smoke`

## Overview

 Build a command-line tool (hlctl) to interact with the Hostlink control plane for task and agent management.

 **Key Features:**

- No authentication (MVP - open API)
- JSON output (modular design for future formats)
- Agent targeting: broadcast to all agents OR filter by fingerprint/tags
- Task creation: inline commands OR script files
- Task and agent management commands

 ---

## Phase 1: Foundation

### Task 1: Set up hlctl project structure ✅

 **Goal**: Create basic CLI application with help and version commands.

 **Files to create:**

- `cmd/hlctl/main.go`
- `cmd/hlctl/commands/root.go`
- `go.mod` updates (add urfave/cli dependency)

 **Success Criteria:**

- [x] `hlctl --help` shows available commands
- [x] `hlctl --version` shows version number
- [x] Project builds successfully: `go build -o hlctl cmd/hlctl/main.go`

 **Tests:**

- **Unit (30%)**: Test command registration and flag parsing ✅ 3/3 passing
- **Integration (50%)**: Test CLI binary execution with --help and --version ✅ 3/3 passing
- **Smoke (20%)**: Manual verification of help output ✅ 1/1 passing

 **Dependencies:** None

 ---

### Task 2: Configuration management ✅

 **Goal**: Support server URL configuration via file and environment variable.

 **Files to create:**

- `cmd/hlctl/config/config.go`
- `cmd/hlctl/config/config_test.go`

 **Success Criteria:**

- [x] Reads `~/.hostlink/config.yml` if exists
- [x] Reads `HOSTLINK_SERVER_URL` environment variable
- [x] Environment variable overrides config file
- [x] Defaults to `http://localhost:8080` if neither set
- [x] Config file format: `server: http://example.com`

 **Tests:**

- **Unit (30%)**: Test config loading logic with mocked filesystem
- **Integration (50%)**: Test config file creation and reading
- **Smoke (20%)**: Manual test with different config sources

 **Dependencies:** Task 1

 ---

## Phase 2: Control Plane API - Tasks

### Task 3: Create task creation endpoint ✅

 **Goal**: API endpoint to create tasks with optional agent targeting.

 **Files created/modified:**

- `app/controller/tasks/tasks.go` (already existed, added validation)
- `app/controller/tasks/tasks_test.go`
- `config/routes.go` (added v2 routes without auth, TODO: remove after proper auth)
- `test/integration/task_creation_test.go`

 **API Spec:**

 ```
 POST /api/v2/tasks (no auth - temporary)
 POST /api/v1/tasks (with auth)
 Body: {
   "command": "ls -la",
   "priority": 1
 }
 Response: {
   "id": "task-123",
   "command": "ls -la",
   "status": "pending",
   "priority": 1,
   "created_at": "2025-10-02T00:00:00Z"
 }
 ```

 **Success Criteria:**

- [x] Creates task via v2 API (no auth)
- [x] Validates required command field
- [x] Validates command syntax using shellwords
- [x] Returns 400 for invalid request
- [x] Returns task ID and status in response

 **Tests:**

- **Unit (30%)**: Test request validation, task creation logic
- **Integration (50%)**: Test full HTTP request/response cycle
  - Create task without filters
  - Create task with fingerprint
  - Create task with tags
  - Create task with both fingerprint and tags
  - Invalid request handling
- **Smoke (20%)**: Test via curl against running server

 **Dependencies:** Task 1, 2

 ---

### Task 4: Create task listing endpoint ✅

 **Goal**: API endpoint to list tasks with optional filtering.
All filtering options are the options present on that particular tables fields.

 **Files to create/modify:**

- `app/controller/tasks/tasks.go` (modified - added priority parsing)
- `domain/task/task.go` (added TaskFilters struct)
- `domain/task/repository.go` (modified FindAll signature to accept filters)
- `internal/repository/gorm/task_repository.go` (implemented filter logic)
- `internal/repository/gorm/task_repository_test.go` (added 4 filter tests)
- `test/integration/task_list_test.go` (created with 9 list tests)
- `test/integration/task_creation_test.go` (fixed database isolation issue)

 **API Spec:**

 ```
 GET /api/v2/tasks?status=pending&priority=2
 Response: [
   {
     "ID": "task-123",
     "Command": "ls -la",
     "Status": "pending",
     "Priority": 2,
     "CreatedAt": "2025-10-02T00:00:00Z",
     ...
   }
 ]
 ```

 **Success Criteria:**

- [x] Lists all tasks without filters
- [x] Filters by status: `?status=pending`
- [x] Filters by priority: `?priority=2` (instead of agent_id - not in schema)
- [x] Combines multiple filters
- [x] Returns empty array if no tasks match
- [x] Returns tasks sorted by created_at DESC

 **Tests:**

- **Unit (30%)**: Test filter logic, query building ✅ 4/4 passing
  - filters_tasks_by_status
  - filters_tasks_by_priority
  - combines_multiple_filters
  - returns_empty_slice_when_no_tasks_match_filters
- **Integration (50%)**: Test full listing scenarios ✅ 9/9 passing
  - lists_all_tasks_without_filters
  - filters_tasks_by_status_pending
  - filters_tasks_by_status_completed
  - filters_tasks_by_status_running
  - filters_tasks_by_status_failed
  - filters_tasks_by_priority
  - combines_status_and_priority_filters
  - returns_empty_array_when_no_tasks_match_filters
  - returns_tasks_sorted_by_created_at_desc
- **Smoke (20%)**: Test via curl against running server (can be tested manually)

 **Dependencies:** Task 3

 ---

### Task 5: Create task details endpoint ✅

 **Goal**: API endpoint to get full task details including output.

 **Files created/modified:**

- `app/controller/tasks/tasks.go` (added Get handler)
- `app/controller/tasks/tasks_test.go` (added 3 unit tests)
- `test/integration/task_details_test.go` (created with 4 integration tests)

 **API Spec:**

 ```
 GET /api/v2/tasks/:id
 Response: {
   "id": "task-123",
   "command": "ls -la",
   "status": "completed",
   "priority": 1,
   "agent_id": "agent-456",
   "output": "total 48\ndrwxr-xr-x...",
   "exit_code": 0,
   "created_at": "2025-10-02T00:00:00Z",
   "started_at": "2025-10-02T00:00:05Z",
   "completed_at": "2025-10-02T00:00:10Z"
 }
 ```

 **Success Criteria:**

- [x] Returns full task details for valid task ID
- [x] Returns 404 for non-existent task ID
- [x] Includes output and exit_code when available
- [x] Shows null for pending/running tasks without output

 **Tests:**

- **Unit (30%)**: Test task retrieval logic ✅ 3/3 passing
  - should_return_task_successfully
  - should_return_404_when_task_not_found
  - should_return_500_when_repository_fails
- **Integration (50%)**: Test task details scenarios ✅ 4/4 passing
  - Get existing task
  - Get non-existent task (404)
  - Get task with output
  - Get task without output (pending)
- **Smoke (20%)**: Test via curl against running server (can be tested manually)

 **Dependencies:** Task 4

 ---

## Phase 3: Control Plane API - Agents

### Task 6: Create agent listing endpoint ✅

 **Goal**: API endpoint to list all registered agents.

 **Files to create/modify:**

- `app/controller/agents/agent.go`
- `app/controller/agents/agent_test.go`
- `config/routes.go` (add route)
- `test/integration/agent_api_test.go`
- `test/smoke/agent_api_test.go`

 **API Spec:**

 ```
 GET /api/v1/agents
 Response: [
   {
     "id": "agent-123",
     "fingerprint": "fp-abc-123",
     "status": "active",
     "last_seen": "2025-10-02T00:05:00Z",
     "tags": [
       {"key": "env", "value": "prod"},
       {"key": "region", "value": "us-east-1"}
     ],
     "registered_at": "2025-10-01T00:00:00Z"
   }
 ]
 ```

 **Success Criteria:**

- [x] Lists all registered agents
- [x] Includes agent metadata (fingerprint, tags, status)
- [x] Shows last_seen timestamp
- [x] Returns empty array if no agents registered
- [x] Returns agents sorted by last_seen DESC
- [x] Filters by status query parameter
- [x] Filters by fingerprint query parameter

 **Tests:**

- **Unit (30%)**: Test agent listing logic ✅
  - Repository: 7/7 tests passing (FindAll method)
  - Controller: 6/6 tests passing (List endpoint)
- **Integration (50%)**: Test agent listing scenarios ✅ 7/7 tests passing
  - List all agents
  - List when no agents exist
  - Verify tags are included
  - Verify sorting by last_seen
  - Filter by status
  - Filter by fingerprint
  - Combined filters
- **Smoke (20%)**: Golang test with build tag ✅ 3/3 tests created
  - Run with: `go test -tags=smoke ./test/smoke -run TestAgentListSmoke`

 **Dependencies:** Task 5

 ---

### Task 7: Create agent details endpoint ✅

 **Goal**: API endpoint to get agent details with recent tasks.

 **Files to create/modify:**

- `app/controller/agents/agents.go` (add Show method to existing file - don't create new files)
- `app/controller/agents/agents_test.go` (add Show tests to existing file - don't create new files)
- `test/integration/agent_details_test.go`
- `test/smoke/agent_details_test.go` (golang test with `//go:build smoke` tag)

 **API Spec:**

 ```
 GET /api/v1/agents/:id
 Response: {
   "id": "agt_123",
   "fingerprint": "fp-abc-123",
   "status": "active",
   "last_seen": "2025-10-02T00:05:00Z",
   "tags": [{"key": "env", "value": "prod"}],
   "registered_at": "2025-10-01T00:00:00Z"
 }
 ```

 **Success Criteria:**

- [x] Returns full agent details for valid agent ID
- [x] Returns 404 for non-existent agent ID
- [x] Includes tags in response
- [x] Returns all required fields with snake_case JSON

 **Tests:**

- **Unit (30%)**: Test agent retrieval logic ✅ 3/3 passing
  - returns agent successfully
  - returns 404 when agent not found
  - returns 500 when repository fails
- **Integration (50%)**: Test agent details scenarios ✅ 4/4 passing
  - gets existing agent
  - returns 404 for non existent agent
  - gets agent with tags
  - returns all required fields
- **Smoke (20%)**: Golang test with `//go:build smoke` tag ✅ 3/3 created
  - creates agent then fetches by ID
  - verifies response structure
  - handles non existent agent
  - Run with: `go test -tags=smoke ./test/smoke -run TestAgentDetailsSmoke`

 **Dependencies:** Task 6

 **Notes:**

- Changed from AID to ID (string) as primary key
- Removed recent_tasks feature (will be added in Phase 6 when task-agent relationship is implemented)
- Using existing FindByID repository method
- **BREAKING CHANGE**: Schema changed - delete `hostlink.db` and `hostlink-dev.db` before running server or smoke tests

 ---

## Phase 4: CLI - Task Commands

### Task 8: Implement `hlctl task create` ✅

 **Goal**: CLI command to create tasks via control plane API.

 **Files created:**

- `cmd/hlctl/commands/task.go` ✅
- `cmd/hlctl/commands/task_test.go` ✅
- `cmd/hlctl/client/client.go` (HTTP client) ✅
- `cmd/hlctl/client/client_test.go` ✅
- `cmd/hlctl/output/formatter.go` (JSON output formatter) ✅
- `cmd/hlctl/output/formatter_test.go` ✅
- `test/integration/hlctl_task_test.go` ✅
- `test/smoke/hlctl_task_test.go` ✅
- Updated `cmd/hlctl/commands/root.go` (added --server flag) ✅
- Updated `cmd/hlctl/main.go` (added error printing) ✅

 **Command Spec:**

 ```bash
 # Inline command
 hlctl task create --command "ls -la"

 # From file
 hlctl task create --file script.sh

 # With agent filters (NOTE: --fingerprint removed, only --tag supported)
 hlctl task create --command "ls" --tag env=prod
 hlctl task create --command "ls" --tag env=prod --tag region=us

 # With priority
 hlctl task create --command "ls" --priority 5

 # With custom server
 hlctl task create --command "ls" --server http://custom:8080

 # Output
 {"id":"task-123","status":"pending","created_at":"2025-10-02T00:00:00Z"}
 ```

 **Success Criteria:**

- [x] `--command` flag creates task with inline command
- [x] `--file` flag reads file and creates task
- [x] Cannot use both `--command` and `--file` (validation error)
- [x] Must provide either `--command` or `--file` (validation error)
- [x] `--tag` flag filters by tags (repeatable) - resolves to agent IDs via API
- [x] `--priority` flag sets task priority (default: 1)
- [x] No filters = broadcasts to all agents
- [x] Outputs JSON with task ID
- [x] Shows error message on API failure
- [x] `--server` flag overrides config server URL

 **Tests:**

- **Unit (20%)**: ✅ 21/21 passing
  - Client tests: 12/12 passing (CreateTask, ListAgents)
  - Output formatter tests: 6/6 passing (JSON formatting)
  - Command tests: 3/3 passing (readScriptFile)
- **Integration (50%)**: ✅ 13/13 passing
  - Create task with --command
  - Create task with --file
  - Create task with --tag (single and multiple)
  - Create task with --priority
  - Create task without filters
  - Error: both --command and --file
  - Error: neither --command nor --file
  - Error: API unreachable
  - Error: file does not exist
  - Verify JSON output
- **Smoke (30%)**: ✅ 5/5 golang tests with `//go:build smoke` tag (run with: `go test -tags=smoke ./test/smoke -run TestTaskCreateSmoke`)
  - With command
  - With file
  - With tags
  - Output format validation
  - Invalid input handling

 **Dependencies:** Task 3

 **Notes:**

- Removed `--fingerprint` flag (fingerprint is agent-internal only)
- Agent filtering works by querying GET /api/v1/agents?tag=X and resolving to agent IDs
- Task creation sends agent_ids array to POST /api/v2/tasks

 ---

### Task 9: Implement `hlctl task list` ✅

 **Goal**: CLI command to list tasks with optional filters.

 **Files created/modified:**

- `cmd/hlctl/commands/task.go` (added list subcommand) ✅
- `cmd/hlctl/client/client.go` (added ListTasks method) ✅
- `cmd/hlctl/client/client_test.go` (added 6 unit tests) ✅
- `test/integration/hlctl_task_test.go` (added 5 integration tests) ✅
- `test/smoke/hlctl_task_test.go` (added 5 smoke tests) ✅

 **Command Spec:**

 ```bash
 # List all tasks
 hlctl task list

 # Filter by status
 hlctl task list --status pending

 # Filter by agent
 hlctl task list --agent agent-123

 # Multiple filters
 hlctl task list --status completed --agent agent-123

 # Output
 [
   {"id":"task-123","command":"ls -la","status":"pending","created_at":"..."}
 ]
 ```

 **Success Criteria:**

- [x] Lists all tasks without filters
- [x] `--status` flag filters by status
- [x] `--agent` flag filters by agent ID
- [x] Combines multiple filters
- [x] Outputs JSON array
- [x] Shows empty array if no tasks match

 **Tests:**

- **Unit (20%)**: Test query parameter building ✅ 6/6 passing
  - TestListTasks_WithoutFilters
  - TestListTasks_WithStatusFilter
  - TestListTasks_WithAgentFilter
  - TestListTasks_WithMultipleFilters
  - TestListTasks_ParsesResponse
  - TestListTasks_HandlesAPIError
- **Integration (50%)**: Test full CLI → API flow ✅ 5/5 passing
  - List all tasks
  - Filter by status
  - Filter by agent
  - Multiple filters
  - Empty results
- **Smoke (30%)**: Test against running server (golang tests with `//go:build smoke` tag) ✅ 5/5 created
  - TestTaskListSmoke_WithoutFilters
  - TestTaskListSmoke_WithStatusFilter
  - TestTaskListSmoke_WithAgentFilter
  - TestTaskListSmoke_OutputFormat
  - TestTaskListSmoke_InvalidInput
  - Run with: `go test -tags=smoke ./test/smoke -run TestTaskListSmoke`

 **Dependencies:** Task 4, 8

 ---

### Task 10: Implement `hlctl task get` ✅

 **Goal**: CLI command to get task details.

 **Files created/modified:**

- `cmd/hlctl/commands/task.go` (added get subcommand) ✅
- `cmd/hlctl/client/client.go` (added GetTask method and TaskDetails struct) ✅
- `cmd/hlctl/client/client_test.go` (added 3 unit tests) ✅
- `test/integration/hlctl_task_test.go` (added 4 integration tests) ✅
- `test/smoke/hlctl_task_test.go` (added 3 smoke tests) ✅

 **Command Spec:**

 ```bash
 # Get task details
 hlctl task get task-123

 # Output
 {
   "id":"task-123",
   "command":"ls -la",
   "status":"completed",
   "output":"total 48\n...",
   "exit_code":0,
   "created_at":"...",
   "completed_at":"..."
 }
 ```

 **Success Criteria:**

- [x] Gets task details for valid task ID
- [x] Shows error for non-existent task ID
- [x] Outputs JSON with full details
- [x] Includes output and exit_code when available

 **Tests:**

- **Unit (20%)**: ✅ 3/3 passing
  - TestGetTask_WithValidID
  - TestGetTask_With404Response
  - TestGetTask_HandlesAPIError
- **Integration (50%)**: ✅ 4/4 passing
  - TestTaskGet_WithExistingTask
  - TestTaskGet_WithNonExistentTask
  - TestTaskGet_WithOutput
  - TestTaskGet_PendingTaskWithoutOutput
- **Smoke (30%)**: ✅ 3/3 created (golang tests with `//go:build smoke` tag)
  - TestTaskGetSmoke_WithExistingTask
  - TestTaskGetSmoke_WithNonExistentTask
  - TestTaskGetSmoke_OutputFormat
  - Run with: `go test -tags=smoke ./test/smoke -run TestTaskGetSmoke`

 **Dependencies:** Task 5, 8

 ---

## Phase 5: CLI - Agent Commands

### Task 11: Implement `hlctl agent list` ✅

 **Goal**: CLI command to list agents.

 **Files created:**

- `cmd/hlctl/commands/agent.go` ✅
- `cmd/hlctl/commands/agent_test.go` ✅
- `test/integration/hlctl_agent_test.go` ✅
- `test/smoke/hlctl_agent_test.go` ✅

 **Command Spec:**

 ```bash
 # List all agents
 hlctl agent list

 # Output
 [
   {
     "id":"agent-123",
     "status":"active",
     "tags":[{"key":"env","value":"prod"}],
     "last_seen":"..."
   }
 ]
 ```

 **Success Criteria:**

- [x] Lists all registered agents
- [x] Outputs JSON array
- [x] Shows empty array if no agents registered
- [x] Includes tags in output

 **Tests:**

- **Unit (20%)**: Test request building ✅ 1/1 passing
- **Integration (50%)**: Test full CLI → API flow ✅ 3/3 passing
  - List all agents
  - List when no agents exist
  - Verify tags included
- **Smoke (30%)**: Test against running server (golang tests with `//go:build smoke` tag) ✅ 3/3 created
  - Run with: `go test -tags=smoke ./test/smoke -run TestAgentListSmoke`

 **Dependencies:** Task 6, 8

 **Notes:**

- Fingerprint excluded from output (user requirement)
- Agent struct enhanced with Status, LastSeen, and Tags fields

 ---

### Task 12: Implement `hlctl agent get` ✅

 **Goal**: CLI command to get agent details.

 **Files created/modified:**

- `cmd/hlctl/commands/agent.go` (added get subcommand) ✅
- `cmd/hlctl/commands/agent_test.go` (added get tests) ✅
- `cmd/hlctl/client/client.go` (added GetAgent method, enhanced Agent struct) ✅
- `test/integration/hlctl_agent_test.go` (added get tests) ✅
- `test/smoke/hlctl_agent_test.go` (added get smoke tests) ✅

 **Command Spec:**

 ```bash
 # Get agent details
 hlctl agent get agent-123

 # Output
 {
   "id":"agent-123",
   "status":"active",
   "last_seen": "...",
   "tags":[{"key":"env","value":"prod"}],
   "registered_at":"..."
 }
 ```

 **Success Criteria:**

- [x] Gets agent details for valid agent ID
- [x] Shows error for non-existent agent ID
- [x] Outputs JSON with full details
- [x] Includes tags in output

 **Tests:**

- **Unit (20%)**: Test agent ID validation ✅ 2/2 passing
- **Integration (50%)**: Test full CLI → API flow ✅ 4/4 passing
  - Get existing agent
  - Get non-existent agent
  - Get agent with tags
  - Get agent without tags
- **Smoke (30%)**: Test against running server (golang tests with `//go:build smoke` tag) ✅ 3/3 created
  - Run with: `go test -tags=smoke ./test/smoke -run TestAgentGetSmoke`

 **Dependencies:** Task 7, 11

 **Notes:**
 - Uses existing `Agent` struct (enhanced with `RegisteredAt` field)
 - No fingerprint in output (per user requirement)
 - Recent tasks feature excluded (will be added in Phase 6 per Task 7 notes)

 ---

## Phase 6: Agent Output Capture

### Task 13: Ensure agent captures and reports output ⏳

 **Goal**: Verify agents capture stdout/stderr and send to control plane.

 **Files to verify/modify:**

- `app/services/taskfetcher/taskfetcher.go` (verify output capture)
- Agent task execution code (verify stdout/stderr capture)
- Task completion endpoint (verify accepts output)
- `test/integration/agent_output_test.go`
- `test/smoke/agent_output_test.go`

 **Success Criteria:**

- [ ] Agent captures stdout from command execution
- [ ] Agent captures stderr from command execution
- [ ] Agent sends output to control plane when completing task
- [ ] Control plane stores output in task record
- [ ] Output retrievable via GET /api/v2/tasks/:id
- [ ] Output retrievable via `hlctl task get`

 **Tests:**

- **Unit (20%)**: Test output capture logic in isolation
- **Integration (50%)**: Test full flow
  - Create task with command that produces stdout
  - Create task with command that produces stderr
  - Create task with command that produces both
  - Verify output stored in database
  - Verify output returned via API
  - Verify output shown in hlctl
- **Smoke (30%)**: Test with real commands against running server (golang tests with `//go:build smoke` tag)

 **Dependencies:** Task 10

 ---

## Phase 7: Integration & Documentation

### Task 14: End-to-end integration tests ⏳

 **Goal**: Comprehensive integration tests for complete workflows.

 **Files to create:**

- `test/integration/e2e_hlctl_test.go`

 **Test Scenarios:**

 1. **Full task lifecycle:**
    - Create task via hlctl
    - Agent polls and executes
    - Verify output via hlctl task get

 2. **Agent filtering:**
    - Register 2 agents with different tags
    - Create task targeting specific tag
    - Verify only matching agent executes

 3. **Multiple tasks:**
    - Create 5 tasks with different priorities
    - Verify agents execute in priority order

 4. **Error scenarios:**
    - Create task with failing command
    - Verify exit_code captured
    - Verify stderr captured

 5. **Agent management:**
    - List agents shows registered agents
    - Get agent shows recent tasks

 6. **File-based task:**
    - Create task from script file
    - Verify full script executed
    - Verify output captured

 **Success Criteria:**

- [ ] All 6 scenarios pass
- [ ] Tests can run against fresh database
- [ ] Tests clean up after themselves
- [ ] Tests document expected behavior

 **Tests:**

- **Integration (100%)**: All tests are integration tests

 **Dependencies:** Task 13

 ---

### Task 15: Documentation ⏳

 **Goal**: Update documentation with hlctl usage and examples.

 **Files to create/modify:**

- `README.md` (add hlctl section)
- `CONTRIBUTING.md` (add hlctl development)
- `docs/hlctl.md` (new - detailed guide)

 **Content to add:**

 1. **README.md:**
    - Quick start with hlctl
    - Installation instructions
    - Basic usage examples

 2. **CONTRIBUTING.md:**
    - Building hlctl: `go build -o hlctl cmd/hlctl/main.go`
    - Running hlctl tests
    - Adding new commands

 3. **docs/hlctl.md:**
    - Full command reference
    - Examples for common workflows
    - Configuration options
    - Output format details

 **Success Criteria:**

- [ ] README has hlctl quick start
- [ ] CONTRIBUTING has hlctl dev guide
- [ ] docs/hlctl.md has complete reference
- [ ] All examples tested and working
- [ ] Documentation covers all implemented features

 **Tests:**

- **Smoke (100%)**: Manual verification of all examples

 **Dependencies:** Task 14

 ---

## Summary

 **Total Tasks:** 15
 **Estimated Completion:** 15 tasks × focused sessions

 **Testing Breakdown:**

- Integration tests: ~50% (focused on full workflows)
- Smoke tests: ~30% (golang tests with `//go:build smoke` tag against running server)
- Unit tests: ~20% (individual components)

 **Key Milestones:**

- After Task 7: Full REST API complete
- After Task 12: Full CLI complete
- After Task 13: Agent integration complete
- After Task 15: MVP ready for use
