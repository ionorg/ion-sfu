# pub-sub-in-browser
Demonstrates the simulcast capabilities of ion-sfu

## Instructions
```
go build cmd/server/json-rpc/main.go
./main -c config.toml
```
### Open pub-sub-in-browser fiddle
Open publisher fiddle [here](https://jsfiddle.net/orlandoco/k38Lyjvg/). First, click "Publish" you should be prompted to allow media access. Once you accept, you will see your local video. Once it is published, open [subscriber](https://jsfiddle.net/orlandoco/jkdq1uow/) fiddle instance and click join. click the layer buttons, and you will change within temporal layers.
