var log = (msg) => {
  console.log(msg);
};

function uuidv4() {
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, function (c) {
    var r = (Math.random() * 16) | 0,
      v = c == "x" ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

const wsuri = "wss://localhost:7000/ws";
const socket = new WebSocket(wsuri);

const config = {
  // iceServers: [{
  //   urls: 'stun:stun.l.google.com:19302'
  // }]
};

let localStream;

// 0. getmedia and setLocalDescription
navigator.mediaDevices
  .getUserMedia({ video: true, audio: true })
  .then((stream) => {
    let el = document.createElement("Video");
    el.srcObject = stream;
    el.autoplay = true;
    el.controls = false;
    el.muted = true;
    document.getElementById("local").appendChild(el);
    localStream = stream;
  })
  .catch(log);

const pc = new RTCPeerConnection(config);

pc.oniceconnectionstatechange = (e) =>
  log(`ICE connection state: ${pc.iceConnectionState}`);

pc.ontrack = function ({ track, streams }) {
  log("ontrack");
  if (track.kind === "video") {
    log("got track");
    track.onunmute = () => {
      let el = document.createElement(track.kind);
      el.srcObject = streams[0];
      el.autoplay = true;

      document.getElementById("remote").appendChild(el);
    };
  }
};

pc.onicecandidate = (event) => {
  log("onicecandidate, sending");
  log(event.candidate);
  if (event.candidate !== null) {
    socket.send(
      JSON.stringify({
        method: "trickle",
        params: {
          candidate: event.candidate,
        },
      })
    );
  }
};

socket.addEventListener("message", async (event) => {
  const resp = JSON.parse(event.data);

  // Listen for server renegotiation notifications
  if (!resp.id && resp.method === "offer") {
    log(`Got offer notification`);
    await pc.setRemoteDescription(resp.params);
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);

    const id = uuidv4();
    log(`Sending answer`);
    socket.send(
      JSON.stringify({
        method: "answer",
        params: { desc: answer },
        id,
      })
    );
  }
});

const join = async () => {
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  const id = uuidv4();

  log("Sending join");
  log(pc.localDescription);
  socket.send(
    JSON.stringify({
      method: "join",
      params: { sid: "test room", offer: pc.localDescription },
      id,
    })
  );

  socket.addEventListener("message", (event) => {
    const resp = JSON.parse(event.data);
    if (resp.id === id) {
      log(`Received publish answer`);

      // Hook this here so it's not called before joining
      pc.onnegotiationneeded = async function () {
        log("onnegotiationneeded");
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        const id = uuid.v4();
        socket.send(
          JSON.stringify({
            method: "offer",
            params: { desc: offer },
            id,
          })
        );

        socket.addEventListener("message", (event) => {
          const resp = JSON.parse(event.data);
          if (resp.id === id) {
            log(`Got renegotiation answer`);
            pc.setRemoteDescription(resp.result);
          }
        });
      };

      log(`setRemoteDescription`);
      // log(resp)
      pc.setRemoteDescription(resp.result);
    } else if (resp.method == "trickle") {
      log("receive trickle");
      pc.addIceCandidate(resp.params);
    }
  });
};

// click pub button
window.Pub = () => {
  log("Publishing stream");

  localStream.getTracks().forEach((track) => {
    pc.addTrack(track, localStream);
  });

  join();
};
