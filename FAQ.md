# Frequenty Asked Questions

### Can I / should I build on `ion-sfu`?
 
`ion-sfu` is considered "stable beta", and you can safely build on it. Many features are still under development, and the API is subject to change rapidly in future releases. If you want to build production-ready apps with ion-sfu, you need a strong grasp of WebRTC fundamentals and modern web development, and some experience with pion and golang is strongly recommended. You'll need to design your own Application Layer, responsible for transmitting signalling messages, validating client input, authenticating your users, administering room membership, and orchestrating scaling decisions.

### Is `ion-sfu` actively maintained or supported? Can I get help?

`ion-sfu` actively developed and supported by an awesome community of volunteers! As an open source project in the pion community, the people who support this project are just the same volunteers who've made time to build and release it. You can join us in the gophers slack in #ion, we have a helpful and positive community and many people are always glad to talk about the project! Once you learn and use `ion-sfu`, you can help make the community better, too!

### What happened to the `cmd/signal` servers?

The Signalling server implementations in `cmd/signal` were moved to the `examples/` folder. If you want to build on `ion-sfu`, you should build your own Application Server, and the `examples/` are a great place to start.

### What do you mean by "Application Server"?
    
Ion-sfu is designed to be a building block that forwards video streams, not a finished product. It contains no mechanisms for validating user accounts or published streams; you must ensure that UIDs are not duplicated or spoofed. You must parse all incoming Offer signals to parse the SDP, and determine if the expected audio codecs are being sent, or the video is of a supported resolution. If a node has too many users, maybe you want to shard your sessions across a cluster of `ion-sfu` -- scaling decisions are left up to you.

### Community ion-SFU implementations
If you don't want to build an Application Server, check out these community projects:

 + `pion/ion` project (Work-in-Progress) alpha/pre-release, uses grpc-web and custom protobufs for signalling
 + `cryptagon/ion-cluster` project (maintained by Tandem.io), uses jsonrpc for signalling

### How do I build the client?
 
Use one of the official SDKs:
 - Browser: [ion-sdk-js](https://github.com/pion/ion-sdk-js)
 - Go application: [ion-sdk-go](https://github.com/pion/ion-sdk-go)
 - Mobile: [ion-sdk-flutter](https://github.com/pion/ion-sdk-flutter)

### How can I join a session receive-only, without sending any media?

If you are using [ion-sdk-js](https://github.com/pion/ion-sdk-js) this should work by default. If not, read on.

If a participant does not have a microphone or camera, or denies access to them from the browser, how can they join? Something needs to be added to the publisher connection to generate valid SDP.

Add a datachannel to the publisher peer connection. e.g. `this.publisher.createDataChannel("ion-sfu")`

### Does `ion-sfu` support audio level indication, telling me who is talking?

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

