.PHONY: test test-it test-smoke test-in-docker dev build build-hlctl pushbuild clean

# Run unit tests
test:
	go test ./...

# Run integration tests
test-it:
	go test -tags=integration ./...

# Run smoke tests
test-smoke:
	go test -tags=integration ./test/smoke/...

# Run tests in Docker
test-in-docker:
	docker build -f dockers/DockerTestfile -t hostlink-test --progress plain --no-cache --target run-test-stage .

# Run development server
dev:
	go run ./...

# Build main binary
build:
	go build -o bin/hostlink

# Build hlctl binary
build-hlctl:
	go build -o bin/hlctl ./cmd/hlctl

# Build and push to remote server
pushbuild:
	GOOS=linux GOARCH=amd64 go build -o hostlink . && scp -O hostlink hlink:~

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f cmd/hlctl/hlctl-test
