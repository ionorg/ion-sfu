const remotesDiv = document.getElementById("remotes");

const config = {
    codec: 'vp8',
    iceServers: [
        {
            "urls": "stun:stun.l.google.com:19302",
        },
        /*{
            "urls": "turn:TURN_IP:3468",
            "username": "username",
            "credential": "password"
        },*/
    ]
};

const signalLocal = new Signal.IonSFUJSONRPCSignal(
    "ws://127.0.0.1:7000/ws"
);

const clientLocal = new IonSDK.Client(signalLocal, config);
signalLocal.onopen = () => clientLocal.join("test room");

clientLocal.ontrack = (track, stream) => {
    console.log("got track", track.id, "for stream", stream.id);
    if (track.kind === "video") {
        track.onunmute = () => {
            const remoteVideo = document.createElement("video");
            remoteVideo.srcObject = stream;
            remoteVideo.autoplay = true;
            remoteVideo.muted = true;
            remotesDiv.appendChild(remoteVideo);

            track.onremovetrack = () => remotesDiv.removeChild(remoteVideo);
        };
    }
};