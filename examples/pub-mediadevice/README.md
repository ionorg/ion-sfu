# Publish camera using Mediadevice library

This example demonstrates how you can read a camera using the [Pion Mediadevice library](https://github.com/pion/mediadevices) and publish the stream to the `ion-sfu` server. It also includes a frontend that can subscribe to the published video.

## Requirements

- A running `ion-sfu` server
- v4l-utils installed on your machine
- [libvpx](https://github.com/pion/mediadevices/wiki/VPX) installed on your machine

## Getting started

Before starting the application, make sure that you have the right camera constraints, including the `FrameFormat` in the `GetUserMedia` function. If you are not sure which settings are right for your camera, reference the `v4l2-ctl --all` command that prints your connected camera's information.

When that is done, you can start the backend that sends the camera image using the `go run` command and provide your `ion-sfu` server's address.

```bash
go run main.go -a localhost:7000
```

The camera stream should now be sent to the `ion-sfu` server. You can verify this by looking at your `ion-sfu` server's logs or by reading the stream using the provided frontend that can be accessed via [jsfiddle](https://jsfiddle.net/mdtvuprj/).
