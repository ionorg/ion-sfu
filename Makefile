GO_LDFLAGS = -ldflags "-s -w"
GO_VERSION = 1.14
GO_TESTPKGS:=$(shell go list ./... | grep -v cmd | grep -v examples)
TEST_UID:=$(shell id -u)
TEST_GID:=$(shell id -g)

all: nodes

deps:
	./scripts/install-run-deps.sh

go_deps:
	go mod download

clean:
	rm -rf bin

upx:
	upx -9 bin/*

example:
	go build -o bin/service-node $(GO_LDFLAGS) examples/service/service-node.go
	go build -o bin/service-watch $(GO_LDFLAGS) examples/watch/service-watch.go

nodes: go_deps
	go build -o bin/sfu $(GO_LDFLAGS) ./cmd/server/grpc/main.go

test: nodes
	go test \
		-coverprofile=cover.out -covermode=atomic \
		-v -race ${GO_TESTPKGS}
