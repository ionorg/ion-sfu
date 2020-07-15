/* eslint-env browser */
let log = msg =>
  document.getElementById('logs').innerHTML += msg + '<br>'

let config = {
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302'
    }
  ]
}

let socket = new WebSocket("ws://localhost:7000/ws");

let localStream
let mid
navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  .then(stream => {
    let el = document.createElement("Video")
    el.srcObject = stream
    el.autoplay = true
    el.controls = true
    el.muted = true
    document.getElementById('localVideos').appendChild(el)

    localStream = stream
  }).catch(log)

window.publish = () => {
  log("Publishing local stream")
  const id = uuid.v4()
  let pc = new RTCPeerConnection(config)
  pc.addStream(localStream)
  pc.createOffer({
    offerToReceiveVideo: false,
    offerToReceiveAudio: false,
  }).then(d => pc.setLocalDescription(d)).catch(log)

  pc.oniceconnectionstatechange = e => log(`ICE connection state: ${pc.iceConnectionState}`)
  pc.onicecandidate = event => {
    if (event.candidate === null) {
      socket.send(JSON.stringify({
        method: "RPC.Publish",
        params: [{
          Rid: "test",
          Offer: pc.localDescription
        }],
        id
      }))
    }
  }

  socket.addEventListener('message', (event) => {
    const resp = JSON.parse(event.data)
    if (resp.id === id) {
      mid = resp.result.Mid
      log(`Publishing local published with mid: ${mid}`)
      pc.setRemoteDescription(resp.result.Answer)
    }
  })
}

window.subscribe = () => {
  log(`Subscribing to remote stream: ${mid}`)
  const id = uuid.v4()
  let pc = new RTCPeerConnection(config)

  pc.addTransceiver("audio", { direction: 'recvonly' });
  pc.addTransceiver("video", { direction: 'recvonly' });
  pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)

  pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)
  pc.onicecandidate = event => {
    if (event.candidate === null) {
      socket.send(JSON.stringify({
        method: "RPC.Subscribe",
        params: [{
          Mid: mid,
          Offer: pc.localDescription
        }],
        id
      }))
    }
  }
  pc.ontrack = function (event) {
    if (event.track.kind === "video") {
      let el = document.createElement(event.track.kind)
      el.srcObject = event.streams[0]
      el.autoplay = true
      el.controls = true

      document.getElementById('remoteVideos').appendChild(el)
    }
  }

  socket.addEventListener('message', (event) => {
    const resp = JSON.parse(event.data)
    if (resp.id === id) {
      log(`Got answer for subscribe`)
      pc.setRemoteDescription(resp.result.Answer)
    }
  })
}

window.startSession = () => {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  try {
    pc.setRemoteDescription(new RTCSessionDescription(JSON.parse(atob(sd))))
  } catch (e) {
    alert(e)
  }
}