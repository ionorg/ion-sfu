const localVideo = document.getElementById("local-video");
const remotesDiv = document.getElementById("remotes");

const serverUrl = "ws://localhost:7000/ws";

/* eslint-env browser */
const joinBtns = document.getElementById("start-btns");

const config = {
  iceServers: [
    {
      urls: "stun:stun.l.google.com:19302",
    },
  ],
};

const signalLocal = new Signal.IonSFUJSONRPCSignal(serverUrl);

const clientLocal = new IonSDK.Client(signalLocal, config);
signalLocal.onopen = () => clientLocal.join("test session");

/**
 * For every remote stream this object will hold the follwing information:
 * {
 *  "id-of-the-remote-stream": {
 *     stream: [Object], // Reference to the stream object
 *     videoElement: [Object] // Reference to the video element that's rendering the stream.
 *   }
 * }
 */
const streams = {};

/**
 * When we click the Enable Audio button this function gets called, and
 * unmutes all the remote videos that might be there and also any future ones.
 * This little party trick is according to
 * https://developer.mozilla.org/en-US/docs/Web/Media/Autoplay_guide.
 */
let remoteVideoIsMuted = true;
function enableAudio() {
  if (remoteVideoIsMuted) {
    // Unmute all the current videoElements.
    for (const streamInfo of Object.values(streams)) {
      let { videoElement } = streamInfo;
      videoElement.pause();
      videoElement.muted = false;
      videoElement.play();
    }
    // Set remoteVideoIsMuted to false so that all future autoplays
    // work.
    remoteVideoIsMuted = false;

    const button = document.getElementById("enable-audio-button");
    button.remove();
  }
}

let localStream;
const start = () => {
  IonSDK.LocalStream.getUserMedia({
    resolution: "vga",
    audio: true,
  })
    .then((media) => {
      localStream = media;
      localVideo.srcObject = media;
      localVideo.autoplay = true;
      localVideo.controls = true;
      localVideo.muted = true;
      joinBtns.style.display = "none";
      clientLocal.publish(media);
    })
    .catch(console.error);
};

clientLocal.ontrack = (track, stream) => {
  console.log("got track", track.id, "for stream", stream.id);
  track.onunmute = () => {
    // If the stream is not there in the streams map.
    if (!streams[stream.id]) {
      // Create a video element for rendering the stream
      remoteVideo = document.createElement("video");
      remoteVideo.srcObject = stream;
      remoteVideo.autoplay = true;
      remoteVideo.muted = remoteVideoIsMuted;
      remotesDiv.appendChild(remoteVideo);

      // Save the stream and video element in the map.
      streams[stream.id] = { stream, videoElement: remoteVideo };

      // When this stream removes a track, assume
      // that its going away and remove it.
      stream.onremovetrack = () => {
        try {
          if (streams[stream.id]) {
            const { videoElement } = streams[stream.id];
            remotesDiv.removeChild(videoElement);
            delete streams[stream.id];
          }
        } catch (err) {}
      };
    }
  };
};

const controlLocalVideo = (radio) => {
  if (radio.value === "false") {
    localStream.mute("video");
  } else {
    localStream.unmute("video");
  }
};

const controlLocalAudio = (radio) => {
  if (radio.value === "false") {
    localStream.mute("audio");
  } else {
    localStream.unmute("audio");
  }
};
