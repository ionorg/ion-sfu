# Publish camera using Mediadevice library

This example demonstrates how you can read a camera using the [Pion Mediadevice library](https://github.com/pion/mediadevices) and publish the stream to the ION-SFU server. It also includes a frontend that can subscribe to the published screen.

## Requirements

- A running ION-SFU server
- v4l-utils installed on your machine
- [libvpx](https://github.com/pion/mediadevices/wiki/VPX) installed on your machine

## Getting started

Before starting the application make sure that you have the right camera constrains including the `FrameFormat` in the `GetUserMedia` function. If you are not sure which settings are right for your camera reference the `v4l2-ctl --all` command which prints the information of your connected camera.

When that is done you can start the backend that sends the camera image using the `go run` command and provide the address of your ION-SFU server.

```bash
go run main.go -a localhost:7000
```

The camera stream should now be send to the ION-SFU server. You can verify this by looking at the logs of your ION-SFU server or by reading the stream using the provided frontend which can be started using Node.js.

```bash
node frontend/server.js
```

You can now visit localhost:3000 and click the `Subscribe` button.
