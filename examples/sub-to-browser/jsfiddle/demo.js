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

pc.addTransceiver("audio", { direction: 'recvonly' });
pc.addTransceiver("video", { direction: 'recvonly' });
pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)

var timer;
var showSDP = () => {
    document.getElementById('localSessionDescription').value = btoa(JSON.stringify(pc.localDescription))
}
pc.onicecandidate = event => {
  clearTimeout(timer);
  if (event.candidate === null) {
    showSDP()
  } else {
    // avoid waiting too long for null, chrome > 30ms, firefox > 10ms
    timer = setTimeout(showSDP, 1000);
  }
}

pc.ontrack = function (event) {
	if (event.track.kind === "video") {
    var el = document.createElement(event.track.kind)
    el.srcObject = event.streams[0]
    el.autoplay = true
    el.controls = true

    document.getElementById('remoteVideos').appendChild(el)
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
