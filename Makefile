.PHONY: test start pushbuild
test:
	go test ./...
test-all:
	docker build -f dockers/DockerTestfile -t hostlink-test --progress plain --no-cache --target run-test-stage .
start:
	go run ./...
pushbuild:
	GOOS=linux GOARCH=amd64 go build -o hostlink . && scp -O hostlink hlink:~
