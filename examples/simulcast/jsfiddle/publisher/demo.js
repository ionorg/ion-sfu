/* eslint-env browser */
const log = msg =>
  document.getElementById('logs').innerHTML += msg + '<br>'

const config = {
  iceServers: [{
    urls: 'stun:stun.l.google.com:19302'
  }]
}

const socket = new WebSocket("ws://localhost:7000/ws");
const pc = new RTCPeerConnection(config)

pc.ontrack = function ({ track, streams }) {
  if (track.kind === "video") {
    log("got track")
    track.onunmute = () => {
      let el = document.createElement(track.kind)
      el.srcObject = streams[0]
      el.autoplay = true

      document.getElementById('remoteVideos').appendChild(el)
    }
  }
}

pc.oniceconnectionstatechange = e => log(`ICE connection state: ${pc.iceConnectionState}`)
pc.onicecandidate = event => {
  if (event.candidate !== null) {
    socket.send(JSON.stringify({
      method: "trickle",
      params: {
        candidate: event.candidate,
      }
    }))
  }
}

socket.addEventListener('message', async (event) => {
  const resp = JSON.parse(event.data)

  // Listen for server renegotiation notifications
  if (!resp.id && resp.method === "offer") {
    log(`Got offer notification`)
    await pc.setRemoteDescription(resp.params)
    const answer = await pc.createAnswer()
    await pc.setLocalDescription(answer)

    const id = uuid.v4()
    log(`Sending answer`)
    socket.send(JSON.stringify({
      method: "answer",
      params: { desc: answer },
      id
    }))
  } else if (resp.method === "trickle") {
    pc.addIceCandidate(resp.params).catch(log);
  }
})

const join = async () => {
  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)
  const id = Math.random().toString()

  socket.send(JSON.stringify({
    method: "join",
    params: { sid: "test room", offer: pc.localDescription },
    id
  }))


  socket.addEventListener('message', (event) => {
    const resp = JSON.parse(event.data)
    if (resp.id === id) {
      log(`Got publish answer`)

      // Hook this here so it's not called before joining
      pc.onnegotiationneeded = async function () {
        log("Renegotiating")
        const offer = await pc.createOffer()
        await pc.setLocalDescription(offer)
        const id = Math.random().toString()
        socket.send(JSON.stringify({
          method: "offer",
          params: { desc: offer },
          id
        }))

        socket.addEventListener('message', (event) => {
          const resp = JSON.parse(event.data)
          if (resp.id === id) {
            log(`Got renegotiation answer`)
            pc.setRemoteDescription(resp.result)
          }
        })
      }

      pc.setRemoteDescription(resp.result)
    }
  })
}

let localStream
let pid
navigator.mediaDevices.getUserMedia({
  video: true,
  audio: true
}).then(stream => {
  let el = document.createElement("Video")
  el.srcObject = stream
  el.autoplay = true
  el.controls = true
  el.muted = true
  document.getElementById('localVideos').appendChild(el)

  localStream = stream
}).catch(log)

window.publish = () => {
  log("Publishing stream")
  localStream.getTracks().forEach((track) => {
    pc.addTrack(track, localStream);
  });

  join()
}
