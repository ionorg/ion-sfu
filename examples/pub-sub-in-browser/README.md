# pub-sub-in-browser
pub-sub-in-browser demonstrates how to send video and/or audio to an ion-sfu from files on disk.

## Instructions
### 1 Local test (non-TLS)
```
go build cmd/server/json-rpc/main.go

#this will listen on non-tls
./main -c config.toml -p 7000
```
### Open pub-sub-in-browser example page
[jsfiddle.net](https://jsfiddle.net/8opdj6s9/) you should be prompted to allow media access. Once you accept, you will see your local video. First, click "Publish" to publish it. Once it is published, click "Subscribe" to subscribe to your published stream.



### 2 Online/LAN Test (TLS)

```
go build cmd/server/json-rpc/main.go

#this will listen on tls
./main -c config.toml -key ./key.pem -cert ./cert.pem -a "0.0.0.0:10000"
```

open [jsfiddle.net](https://jsfiddle.net/8opdj6s9/) change this line, set your domain or ip:

```
let socket = new WebSocket("wss://{yourdomain|yourip}:{yourport}/ws");
```

click save, then click run, this will restart web example.

Then, click "Publish" to publish it. Once it is published, click "Subscribe" to subscribe to your published stream.
