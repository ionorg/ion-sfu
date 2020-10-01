# pub-from-browser

pub-from-browser demonstrates how you can publish a stream to ion-sfu from a browser.

## Instructions

### Download pub-from-browser

```
go get github.com/pion/ion-sfu/examples/pub-from-browser
```

### Open pub-from-browser example page

Open [jsfiddle.net](https://jsfiddle.net/j3yhron4/) and you should see two text-areas and a 'Start Session' button.

### Run publish-from-browser, with your browsers SessionDescription as stdin

In the jsfiddle the top textarea is your browser, copy that and:

#### Linux/macOS

Run `echo $BROWSER_SDP | pub-from-browser $yourroom`

#### Windows

1. Paste the SessionDescription into a file.
1. Run `pub-from-browser $yourroom < my_file`

### Input publish-from-browser's SessionDescription into your browser

Copy the text that `pub-from-browser` just emitted and copy into second text area. This needs to be done quickly to avoid an ICE timeout.

### Hit 'Start Session' in jsfiddle, your video is now being published to the sfu!

Your browser should send video to ion-sfu, you can verify it by looking at ion-sfu logs.
