
var log = msg => {
    console.log(msg)
}

function uuidv4() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
    var r = Math.random() * 16 | 0, v = c == 'x' ? r : (r & 0x3 | 0x8);
    return v.toString(16);
  });
}

const wsuri = "wss://" + location.host + "/ws";
const socket = new WebSocket(wsuri);



const config = {
  // iceServers: [{
  //   urls: 'stun:stun.l.google.com:19302'
  // }]
}

let localStream

// 0. getmedia and setLocalDescription
navigator.mediaDevices.getUserMedia({ video: true, audio: true})
    .then(stream => {
        let el = document.createElement("Video")
        el.srcObject = stream
        el.autoplay = true
        el.controls = false
        el.muted = true
        document.getElementById('local').appendChild(el)
        localStream = stream
    }).catch(log)


const pcPublish = new RTCPeerConnection(config)


pcPublish.oniceconnectionstatechange = e => log(`ICE connection state: ${pcPublish.iceConnectionState}`)

pcPublish.ontrack = function ({ track, streams }) {
  log("ontrack")
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

pcPublish.onicecandidate = event => {
  log("onicecandidate, sending")
  log(event.candidate)
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
  }
})





const join = async () => {
    const offer = await pcPublish.createOffer()
    await pcPublish.setLocalDescription(offer)
    const id = uuidv4()

    log("Sending join")
    log(pcPublish.localDescription)
    socket.send(JSON.stringify({
        method: "join",
        params: { sid: "test room", offer: pcPublish.localDescription },
        id
    }))


    socket.addEventListener('message', (event) => {
        const resp = JSON.parse(event.data)
        if (resp.id === id) {
            log(`Received publish answer`)

            // Hook this here so it's not called before joining
            pcPublish.onnegotiationneeded = async function () {
                log("onnegotiationneeded")
                const offer = await pcPublish.createOffer()
                await pcPublish.setLocalDescription(offer)
                const id = uuid.v4()
                socket.send(JSON.stringify({
                    method: "offer",
                    params: { desc: offer },
                    id
                }))

                socket.addEventListener('message', (event) => {
                    const resp = JSON.parse(event.data)
                    if (resp.id === id) {
                        log(`Got renegotiation answer`)
                        pcPublish.setRemoteDescription(resp.result)
                    }
                })
            }

            log(`setRemoteDescription`)
            // log(resp)
            pcPublish.setRemoteDescription(resp.result)
        } else if (resp.method == "trickle"){
            log("receive trickle")
            // log(resp.params)
            // const candidate: RTCIceCandidateInit = "a="+resp.params.candidate
            pcPublish.addIceCandidate(resp.params)
        }
    })
}


// click pub button
window.Pub = () => {
    log("Publishing stream")

    localStream.getTracks().forEach((track) => {
        pcPublish.addTrack(track, localStream);
    });


    join()
}


// window.Sub = name => {
//     let pcSubcribe = new RTCPeerConnection({
//         // iceServers: [{
//         //     urls: 'stun:stun.l.google.com:19302'
//         // }]
//     })


//     // 1. send subscribe  sdp
//     pcSubcribe.onicecandidate = event => {
//         if (event.candidate === null) {
//             log("SDP chrome ->  sfu:\n" + pcSubcribe.localDescription.sdp)
//             var sendData = {type:'subscribe', sdp:pcSubcribe.localDescription.sdp, name:name}
//             sock.send(JSON.stringify(sendData));
//         }
//     }

//     pcSubcribe.addTransceiver('audio', {'direction': 'recvonly'})
//     pcSubcribe.addTransceiver('video', {'direction': 'recvonly'})

//     pcSubcribe.createOffer()
//         .then(d => pcSubcribe.setLocalDescription(d))
//         .catch(log)

//     // 4. receive data
//     pcSubcribe.ontrack = function (event) {
//         var el = document.getElementById('remote')
//         el.srcObject = event.streams[0]
//         el.autoplay = true
//         el.controls = true
//     }

//     // 3. receive sdp 
//     window.processRcvSDPSubscribe = (sd, name) => {
//         try {
//             pcSubcribe.setRemoteDescription(new RTCSessionDescription({type:'answer', sdp:sd, name:name}))
//         } catch (e) {
//             log(e)
//         }
//     }
// }

