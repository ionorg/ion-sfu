# Using gRPC

## Signaling

For service-to-service communication, you can use the grpc interface. A common pattern is to call the grpc endpoints from a custom signaling service.

### Golang

```
go build ./cmd/signal/grpc/main.go && ./main -c config.toml
```

### Docker

```
docker run -p 50051:50051 -p 5000-5200:5000-5200/udp pionwebrtc/ion-sfu:latest-grpc
```
