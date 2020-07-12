# !/bin/bash

protoc --go_out=plugins=grpc:. --go_opt=paths=source_relative cmd/server/grpc/proto/sfu.proto