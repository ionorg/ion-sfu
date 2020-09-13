#!/bin/bash
go get github.com/golang/protobuf/protoc-gen-go@v1.4.0
go get google.golang.org/grpc/@v1.32.0
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc

protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative cmd/server/grpc/proto/sfu.proto
