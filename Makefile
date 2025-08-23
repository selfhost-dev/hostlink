.PHONY: test start
test:
	go test ./...
test-all:
	docker build -f dockers/DockerTestfile -t hostlink-test --progress plain --no-cache --target run-test-stage .
start:
	go run ./...
