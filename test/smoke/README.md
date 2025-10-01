# Smoke Tests

Smoke tests verify that the application starts correctly and basic functionality works in a production-like environment.

## Running Smoke Tests

These tests require the application to be running. Start the server first:

```bash
go run main.go
```

Then in another terminal, run the smoke tests:

```bash
go test ./test/smoke -v
```

## What Smoke Tests Check

- Application starts without errors
- Health endpoint responds
- Critical endpoints accept valid requests
- Validation is working (rejects invalid requests)

These tests catch issues that unit/integration tests might miss, such as:
- Missing validator registration
- Missing middleware setup
- Configuration issues
- Route registration problems
