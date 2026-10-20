.PHONY: build check fmt
build:
	go build -trimpath -o bin/workbuddy-cli ./cmd/workbuddy-cli
check:
	go test -race ./...
	go vet ./...
fmt:
	gofmt -w cmd internal
