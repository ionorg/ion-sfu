# pub-sub-in-browser
pub-sub-in-browser demonstrates how to send video and/or audio to an ion-sfu from files on disk.

## Instructions
### Start an ion-sfu instance
```
go build cmd/server/json-rpc/main.go
./main -c config.toml
```
### Open pub-sub-in-browser example page
[jsfiddle.net](https://jsfiddle.net/u41ct0jm/) you should be prompted to allow media access. Once you accept, you will see your local video. First, click "Publish" to publish it. Once it is published, click "Subscribe" to subscribe to your published stream.
