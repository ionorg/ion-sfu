# pub-sub-in-browser
Demonstrates how to publish video and/or audio to an `ion-sfu` from the browser using the `json-rpc` interface.

## Instructions
```
go build cmd/server/json-rpc/main.go
./main -c config.toml
```
### Open pub-sub-in-browser fiddle
Open fiddle [here](https://jsfiddle.net/8gcrvojw/) you should be prompted to allow media access. Once you accept, you will see your local video. First, click "Publish" to publish it. Once it is published, open another fiddle instance and click publish. Each fiddle will receive the others stream and display it under remote streams.
