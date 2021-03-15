# Frequenty Asked Questions

### How can I join a session receive-only, without sending any media?

If you are using [ion-sdk-js](https://github.com/pion/ion-sdk-js) this should work by default. If not, read on.

If a participant does not have a microphone or camera, or denies access to them from the browser, how can they join? Something needs to be added to the publisher connection to generate valid SDP.

Add a datachannel to the publisher peer connection. e.g. `this.publisher.createDataChannel("ion-sfu")`

### Does ion-sfu support audio level indication, telling me who is talking?

Yes. The subscriber peer connection will contain a data channel. It gets sent an array of stream id that are making noise, ordered loudest first.

Stream id is the `msid` from the SDP, [defined here](https://tools.ietf.org/html/draft-ietf-mmusic-msid-17). It is up to your application to map that to a meaningful value, such as the user's name.

Here is an example in Typescript:
```
subscriber.ondatachannel = (e: RTCDataChannelEvent) => {
	e.channel.onmessage = (e: MessageEvent) => {
		this.mySpeakingCallback(JSON.parse(e.data));
	}
}
```

Audio level indication can be tuned [here in the configuration file](https://github.com/pion/ion-sfu/blob/master/config.toml#L15-L28).

### Can I record the audio and/or video to disk?

Recording (and general audio/video processing) is provided by separate project [ion-avp](https://github.com/pion/ion-avp/).

