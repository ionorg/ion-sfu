# !/bin/bash

protoc --go_out=plugins=grpc:. --go_opt=paths=source_relative pkg/proto/media/media.proto
protoc --go_out=plugins=grpc:. --go_opt=paths=source_relative pkg/proto/sfu/sfu.proto