# publish-from-browser
publish-from-browser demonstrates how you can publish a stream to ion-sfu from a browser

## Instructions
### Download publish-from-browser
```
go get github.com/pion/ion-sfu/examples/publish-from-browser
```

### Open publish-from-browser example page
[jsfiddle.net](https://jsfiddle.net/j3yhron4/) you should see two text-areas and a 'Start Session' button.

### Run publish-from-browser, with your browsers SessionDescription as stdin
In the jsfiddle the top textarea is your browser, copy that and:
#### Linux/macOS
Run `echo $BROWSER_SDP | publish-from-browser`
#### Windows
1. Paste the SessionDescription into a file.
1. Run `publish-from-browser < my_file`

### Input publish-from-browser's SessionDescription into your browser
Copy the text that `publish-from-browser` just emitted and copy into second text area

### Hit 'Start Session' in jsfiddle, your video is now being published to the sfu!
Your browser should send video to ion-sfu, you can verify it by looking at ion-sfu logs.