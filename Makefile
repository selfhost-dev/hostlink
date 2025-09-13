.PHONY: test start build pushbuild
test:
	go test ./...
test-all:
	docker build -f dockers/DockerTestfile -t hostlink-test --progress plain --no-cache --target run-test-stage .
dev:
	go run ./...
build:
	go build -o bin/hostlink
pushbuild:
	GOOS=linux GOARCH=amd64 go build -o hostlink . && scp -O hostlink hlink:~
