# publish
publish demonstrates how you can publish a stream using ion-sfu

## Instructions
### Download publish
```
go get github.com/pion/ion-sfu/examples/publish
```

### Open publish example page
[jsfiddle.net](https://jsfiddle.net/j3yhron4/) you should see two text-areas and a 'Start Session' button.

### Run publish, with your browsers SessionDescription as stdin
In the jsfiddle the top textarea is your browser, copy that and:
#### Linux/macOS
Run `echo $BROWSER_SDP | publish`
#### Windows
1. Paste the SessionDescription into a file.
1. Run `publish < my_file`

### Input publish's SessionDescription into your browser
Copy the text that `publish` just emitted and copy into second text area

### Hit 'Start Session' in jsfiddle, your video is now being published to the sfu!
Your browser should send video to ion-sfu, you can verify it by looking at ion-sfu logs.