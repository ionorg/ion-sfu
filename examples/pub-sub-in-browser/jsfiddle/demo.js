/* eslint-env browser */
let log = msg =>
  document.getElementById('logs').innerHTML += msg + '<br>'

let config = {
  iceServers: [{
    urls: 'stun:stun.l.google.com:19302'
  }]
}

let socket = new WebSocket("ws://localhost:7000/ws");
let pc = new RTCPeerConnection(config)
pc.ontrack = function (event) {
  log("got track")
  pc.addTransceiver(event.track, {
    direction: 'recvonly'
  });
  let el = document.createElement(event.track.kind)
  el.srcObject = event.streams[0]
  el.autoplay = true
  el.controls = true

  event.track.onmute = function () {
    el.parentNode.removeChild(el);
  }

  document.getElementById('remoteVideos').appendChild(el)
}

let localStream
let pid
navigator.mediaDevices.getUserMedia({
  video: true,
  audio: true
})
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
  pc.addStream(localStream)
  pc.createOffer({
    offerToReceiveVideo: false,
    offerToReceiveAudio: false,
  }).then(d => pc.setLocalDescription(d)).catch(log)

  pc.oniceconnectionstatechange = e => log(`ICE connection state: ${pc.iceConnectionState}`)
  pc.onicecandidate = event => {
    if (event.candidate === null) {
      log("ice gathering complete")
      socket.send(JSON.stringify({
        method: "RPC.Connect",
        params: [{
          Offer: pc.localDescription
        }],
        id
      }))
    }
  }

  socket.addEventListener('message', (event) => {
    const resp = JSON.parse(event.data)
    if (resp.id === id) {
      pid = resp.result.Pid
      log(`Publishing local published with pid: ${pid}`)
      pc.setRemoteDescription(resp.result.Answer)
    }
  })
}

window.subscribe = () => {
  const id = uuid.v4()
  let ssrc = document.getElementById('ssrc').value
  log(`Subscribing to remote ssrc ${ssrc} with peer ${pid}`)

  socket.send(JSON.stringify({
    method: "RPC.Subscribe",
    params: [{
      Pid: pid,
      Ssrcs: [parseInt(ssrc, 10)],
    }],
    id
  }))

  socket.addEventListener('message', (event) => {
    const resp = JSON.parse(event.data)
    if (resp.id === id) {
      log(`Got answer for subscribe ${resp.result.Offer}`)
      pc.setRemoteDescription(resp.result.Offer)
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
