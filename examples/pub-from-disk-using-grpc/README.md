# pub-from-disk-using-grpc
pub-from-disk-using-grpc demonstrates how to send video and/or audio to an ion-sfu from files on disk.

## Instructions
### Create IVF named `output.ivf` that contains a VP8 track and/or `output.ogg` that contains a Opus track
```
ffmpeg -i $INPUT_FILE -g 30 output.ivf
ffmpeg -i $INPUT_FILE -c:a libopus -page_duration 20000 -vn output.ogg
```

### Download pub-from-disk-using-grpc
```
go get github.com/pion/ion-sfu/examples/pub-from-disk-using-grpc
```

### Run pub-from-disk-using-grpc
The `output.ivf` you created should be in the same directory as `pub-from-disk-using-grpc`.

Run `pub-from-disk-using-grpc $yourroom`

Congrats, you are now publishing video to the ion-sfu! Now start building something cool!
