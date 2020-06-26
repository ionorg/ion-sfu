/* eslint-env browser */
let pc = new RTCPeerConnection({
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302'
    }
  ]
})
var log = msg =>
  document.getElementById('logs').innerHTML += msg + '<br>'

navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  .then(stream => {
    var el = document.createElement("Video")
    el.srcObject = stream
    el.autoplay = true
    el.controls = true
    el.muted = true
    document.getElementById('localVideos').appendChild(el)

    pc.addStream(stream)
    pc.createOffer({
      offerToReceiveVideo: false,
      offerToReceiveAudio: false,
    }).then(d => pc.setLocalDescription(d)).catch(log)
  }).catch(log)

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)
pc.onicecandidate = event => {
  if (event.candidate === null) {
    document.getElementById('localSessionDescription').value = btoa(JSON.stringify(pc.localDescription))
  }
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