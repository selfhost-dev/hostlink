# Repoless Agent Implementation Plan

## Overview

Remove agent's direct database dependency by having agents send task updates to control plane API instead of calling `repo.Update()` directly.

## Architecture

- **Current**: Agent → `repo.Update()` → Database
- **New**: Agent → HTTP PUT `/api/v1/tasks/:id` → Control Plane → Database

---

## Phase 1: Control Plane API - Task Update Endpoint

### Task 1: Create Task Update API Endpoint (Control Plane)

**Time: ~15 min | Files: 2**

### Empty Implementation

- Add `Update()` method to `app/controller/tasks/tasks.go`
- Register route `PUT /api/v1/tasks/:id` in `config/routes.go` (with auth middleware)

### Success Criteria

- [ ] Endpoint accepts PUT `/api/v1/tasks/:id`
- [ ] Requires authentication (agentauth middleware)
- [ ] Empty handler returns 200 OK

---

### Task 2: Add Unit Tests for Update Endpoint

**Time: ~15 min | Files: 1**

### Tests in `app/controller/tasks/tasks_test.go`

- [ ] Test: should accept valid update request
- [ ] Test: should validate required fields
- [ ] Test: should return 404 for non-existent task

---

### Task 3: Implement Update Endpoint Logic

**Time: ~15 min | Files: 1**

### Implementation in `app/controller/tasks/tasks.go`

- Parse request body (status, output, error, exit_code)
- Call `taskRepository.Update()`
- Return updated task

### Success Criteria

- [ ] Parses JSON body correctly
- [ ] Updates task in database
- [ ] Returns 200 with updated task
- [ ] Returns 404 if task doesn't exist
- [ ] All unit tests pass

---

### Task 4: Add Integration Tests for Update Endpoint

**Time: ~15 min | Files: 1**

### New file: `test/integration/task_update_test.go`

- [x] Test: agent updates task with output successfully
- [x] Test: agent updates task with exit code 0
- [x] Test: agent updates task with non-zero exit code
- [x] Test: agent updates task with error message
- [x] Test: authenticated request succeeds
- [x] Test: unauthenticated request fails (401)
- [x] Test: update non-existent task returns 404
- [x] Test: update with invalid data returns 400

---

## Phase 2: Agent Side - TaskReporter Service

### Task 5: Create TaskReporter Service (Agent Side)

**Time: ~15 min | Files: 1**

### Empty Implementation

- Create `app/services/taskreporter/taskreporter.go`
- Define `TaskReporter` interface with `Report(taskID, result)` method
- Define `TaskResult` struct (status, output, error, exit_code)
- Create empty `New()` and `NewDefault()` constructors

### Success Criteria

- [x] Interface defined
- [x] Struct with http.Client, signer, controlPlaneURL
- [x] Config struct defined
- [x] Constructors return empty service

---

### Task 6: Add Unit Tests for TaskReporter Constructor

**Time: ~15 min | Files: 1**

### New file: `app/services/taskreporter/taskreporter_test.go`

- [x] Test: should create service with request signer
- [x] Test: should require agent state for agent ID
- [x] Test: should configure HTTP client with timeout
- [x] Test: should use default timeout (30s) when not specified

---

### Task 7: Implement TaskReporter Constructors

**Time: ~15 min | Files: 1**

### Implementation in `app/services/taskreporter/taskreporter.go`

- Implement `New(cfg *Config)` with validation
- Implement `NewDefault()` using appconf
- Set default timeout to 30 seconds

### Success Criteria

- [x] Creates RequestSigner
- [x] Validates agent ID exists
- [x] Sets timeout (default 10s)
- [x] All unit tests pass

---

### Task 8: Add Unit Tests for TaskReporter Report Method

**Time: ~15 min | Files: 1**

### Tests in `app/services/taskreporter/taskreporter_test.go`

- [x] Test: should send PUT request to correct endpoint
- [x] Test: should add authentication headers
- [x] Test: should marshal TaskResult to JSON
- [x] Test: should handle 200 response
- [x] Test: should handle 404 response
- [x] Test: should handle 500 response
- [x] Test: should handle network errors

---

### Task 9: Implement TaskReporter Report Method

**Time: ~15 min | Files: 1**

### Implementation in `app/services/taskreporter/taskreporter.go`

- Marshal TaskResult to JSON
- Create PUT request to `/api/v1/tasks/{taskID}`
- Sign request with RequestSigner
- Execute request
- Handle errors

### Success Criteria

- [x] Sends PUT to `/api/v1/tasks/:id`
- [x] Sets Content-Type: application/json
- [x] Signs request with auth headers
- [x] Returns nil on success
- [x] Returns error on failure
- [x] All unit tests pass

---

## Phase 3: Retry Logic & Resilience

### Task 10: Add Retry Logic to TaskReporter

**Time: ~15 min | Files: 1**

### Implementation in `app/services/taskreporter/taskreporter.go`

- Add `RetryConfig` struct (maxRetries, maxWaitTime)
- Implement exponential backoff (5 retries, max 30 min)
- Make retry config optional in Config struct

### Success Criteria

- [x] Retries up to 5 times
- [x] Uses exponential backoff
- [x] Max wait time is 30 minutes
- [x] Config allows custom retry settings (for tests)

---

### Task 11: Add Unit Tests for Retry Logic

**Time: ~15 min | Files: 1**

### Tests in `app/services/taskreporter/taskreporter_test.go`

- [x] Test: retries on network failure
- [x] Test: retries on 500 error
- [x] Test: does not retry on 404
- [x] Test: does not retry on 400
- [x] Test: uses exponential backoff
- [x] Test: respects max retries (5)
- [x] Test: custom retry config works (for testing)

---

## Phase 4: Integration - Remove Database Dependency

### Task 12: Update TaskJob to Use TaskReporter

**Time: ~15 min | Files: 1**

### Modify `app/jobs/taskjob/taskjob.go`

- Remove `repo task.Repository` parameter from `Register()`
- Create TaskReporter instance
- Replace `repo.Update()` calls with `reporter.Report()`
- Handle reporter errors (log and continue)

### Success Criteria

- [x] No database dependency in taskjob
- [x] Uses TaskReporter for all updates
- [x] Logs errors on failed reports
- [x] Continues processing on report failure

---

### Task 13: Add Integration Tests for TaskJob with Reporter

**Time: ~15 min | Files: 1**

### New file: `test/integration/taskjob_reporter_test.go`

- [x] Test: taskjob sends update via API (not direct DB)
- [x] Test: task output is captured and sent
- [x] Test: exit code is sent correctly
- [x] Test: error messages are sent
- [x] Test: authentication headers are included
- [x] Test: failed update is logged (no crash)

---

### Task 14: Update Main to Remove Repository Dependency

**Time: ~10 min | Files: 1**

### Modify `main.go`

- Change `taskjob.Register(container.TaskRepository)` to `taskjob.Register()`

### Success Criteria

- [x] Compiles without errors
- [x] Server starts successfully
- [x] No repository passed to taskjob

---

## Phase 5: End-to-End Testing

### Task 15: Add Smoke Tests for End-to-End Flow

**Time: ~15 min | Files: 1**

### New file: `test/smoke/agent_task_update_test.go`

- [ ] Test: create task via API
- [ ] Test: agent fetches task
- [ ] Test: agent executes task
- [ ] Test: agent reports result via API
- [ ] Test: verify task updated in database
- [ ] Test: verify output captured correctly
- [ ] Test: hlctl task get shows output

---

## Testing Distribution

- **Unit Tests**: 20% (~35 tests across tasks 2, 6, 8, 11)
- **Integration Tests**: 50% (~15 tests across tasks 4, 13)
- **Smoke Tests**: 30% (~7 tests in task 15)

## Total Time Estimate

15 tasks × 15 min = ~3.75 hours

## Dependencies

- Tasks 1-4: Update endpoint (sequential)
- Tasks 5-9: TaskReporter service (sequential)
- Tasks 10-11: Retry logic (sequential, depends on 5-9)
- Task 12: Integration (depends on 1-4 and 5-11)
- Tasks 13-15: Testing (depends on 12)
