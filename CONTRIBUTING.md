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

### Questions or Issues?

Feel free to open an issue on GitHub or reach out to the maintainers.
