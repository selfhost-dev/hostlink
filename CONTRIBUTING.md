# Contributing to Hostlink

Thank you for your interest in contributing to Hostlink!

## Development Setup

### Prerequisites

- Go 1.23 or higher
- SQLite (included in most systems)

### Running Locally

1. **Clone the repository**
   ```bash
   git clone https://github.com/selfhost-dev/hostlink.git
   cd hostlink
   ```

2. **Set up environment (optional)**

   Copy the example environment file:
   ```bash
   cp .env.example .env
   ```

   The application will automatically load `.env` file on startup.

   Default configuration works out of the box:
   - Server runs on `localhost:8080`
   - SQLite database: `hostlink-dev.db`
   - Agent data stored in `.local/` directory

3. **Create local data directory**
   ```bash
   mkdir -p .local
   ```

4. **Run the application**
   ```bash
   go run main.go
   ```

   The server will start at `http://localhost:8080`

5. **Verify it's running**
   ```bash
   curl http://localhost:8080/health
   ```

### Running Tests

**Run all tests:**
```bash
go test ./...
```

**Run unit tests only:**
```bash
go test ./app/... ./domain/... ./internal/...
```

**Run integration tests:**
```bash
go test -tags=integration ./test/integration/...
```

**Run smoke tests:**
```bash
go test -tags=smoke ./test/smoke/...
```

**Run tests with coverage:**
```bash
go test -cover ./...
```

### Development Workflow

1. Make your changes
2. Run tests to ensure everything works
3. Run `go fmt ./...` to format code
4. Commit your changes following conventional commits
5. Push and create a pull request

### Environment Variables

See `.env.example` for all available configuration options.

Key variables for development:
- `APP_ENV` - Set to `development` (default)
- `SH_APP_PORT` - Server port (default: 8080)
- `SH_DB_URL` - Database URL (default: `file:hostlink-dev.db`)

## Working on hlctl

The `hlctl` CLI tool is located in `cmd/hlctl/`.

### Building hlctl

```bash
go build -o hlctl cmd/hlctl/main.go
```

Or build and install to `$GOPATH/bin`:
```bash
go install ./cmd/hlctl
```

### Running hlctl tests

**Unit tests:**
```bash
go test ./cmd/hlctl/...
```

**Integration tests:**
```bash
go test -tags=integration ./test/integration -run TestHlctl
```

**Smoke tests (requires running server):**
```bash
# Start the server first
go run main.go

# In another terminal
go test -tags=smoke ./test/smoke -run TestHlctl
```

## Release Process

Releases are automated via GitHub Actions and triggered by pushing a version tag.

### Creating a Release

1. Ensure all changes are merged to `main`
2. Create and push a version tag:
   ```bash
   git tag v1.2.3
   git push origin v1.2.3
   ```
3. The CI workflow will automatically:
   - Build binaries for linux/amd64 and linux/arm64
   - Create a **draft** release with all assets
   - Verify all assets are valid (checksums, gzip integrity, binary execution)
   - Publish the release (make it visible)

4. Monitor the [Actions tab](https://github.com/selfhost-dev/hostlink/actions) for workflow status

### Important Notes

- **Do NOT create releases manually via GitHub UI.** The automated workflow ensures all release assets are verified before becoming visible to users. Manual releases bypass this verification and may cause installation failures.
- Tags must follow semver format: `v1.0.0`, `v1.2.3`, etc.
- If verification fails, the release remains as a draft for debugging. Check the workflow logs and re-trigger if needed.

### Questions or Issues?

Feel free to open an issue on GitHub or reach out to the maintainers.
