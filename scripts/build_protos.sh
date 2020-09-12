# !/bin/bash

#you must install the right version
#protoc install:
#https://github.com/protocolbuffers/protobuf/releases/tag/v3.12.4

#protoc-gen-go-grpc install:
#go install google.golang.org/grpc/cmd/protoc-gen-go-grpc

protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative cmd/server/grpc/proto/sfu.proto
