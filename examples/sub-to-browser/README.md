# sub-to-browser
sub-to-browser demonstrates how you can subscribe to a stream from ion-sfu

## Instructions
### Download sub-to-browser
```
go get github.com/pion/ion-sfu/examples/sub-to-browser
```

### Open sub-to-browser example page
[jsfiddle.net](https://jsfiddle.net/04r7xbLa/) you should see two text-areas and a 'Start Session' button.

### Run sub-to-browser, with your browsers SessionDescription as stdin
In the jsfiddle the top textarea is your browser, copy that and:
#### Linux/macOS
Run `echo $BROWSER_SDP | sub-to-browser $MID`
#### Windows
1. Paste the SessionDescription into a file.
1. Run `sub-to-browser $MID < my_file`

### Input sub-to-browser's SessionDescription into your browser
Copy the text that `sub-to-browser` just emitted and copy into second text area

### Hit 'Start Session' in jsfiddle, your video is now being stream from the sfu!
Your browser should render video it is receiving from the ion-sfu.
