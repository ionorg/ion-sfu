# sub-to-disk-using-grpc
sub-to-disk-using-grpc demonstrates how to subscribe a stream from ion-sfu, and save VP8/Opus to disk.

## Instructions
### Download sub-to-disk-using-grpc
```
export GO111MODULE=on
go get github.com/pion/ion-sfu/examples/sub-to-disk-using-grpc
```

### Run sub-to-disk-using-grpc
The `output.ivf` and/or `output.ogg` you created should be in the same directory as `sub-to-disk-using-grpc`.

Run `sub-to-disk-using-grpc $yourroom`

Congrats, you are now publishing video to the ion-sfu! Now start building something cool!
