GO_LDFLAGS = -ldflags "-s -w"
GO_VERSION = 1.14
GO_TESTPKGS:=$(shell go list ./... | grep -v cmd | grep -v examples)

all: nodes

go_init:
	go mod download
	go generate ./...

clean:
	rm -rf bin


build_grpc: go_init
	go build -o bin/sfu-grpc $(GO_LDFLAGS) ./cmd/signal/grpc/main.go

build_jsonrpc: go_init
	go build -o bin/sfu-json-rpc $(GO_LDFLAGS) ./cmd/signal/json-rpc/main.go

build_allrpc: go_init
	go build -o bin/sfu-allrpc $(GO_LDFLAGS) ./cmd/signal/allrpc/main.go

test: go_init
	go test \
		-timeout 240s \
		-coverprofile=cover.out -covermode=atomic \
		-v -race ${GO_TESTPKGS} 
