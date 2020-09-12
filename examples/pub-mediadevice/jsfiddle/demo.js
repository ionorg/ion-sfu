const log = msg =>
    document.getElementById('logs').innerHTML += msg + '<br>'

const config = {
    iceServers: [
        {
            "urls": "stun:stun.l.google.com:19302",
        },
        /*{
            "urls": "turn:TURN_IP?transport=tcp",
            "username": "username",
            "credential": "password"
        },*/
    ]
};

const wsuri = `ws://localhost:7000/ws`
const socket = new WebSocket(wsuri);
const pc = new RTCPeerConnection(config)

const video = document.querySelector("video");

pc.ontrack = function ({ track, streams }) {
    if (track.kind === "video") {
        log("got track")
        track.onunmute = () => {
            video.srcObject = streams[0]
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

        const id = uuidv4()
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
    const offerOption = {
        offerToReceiveAudio: true,
        offerToReceiveVideo: true
    }

    pc.addTransceiver('audio', {'direction': 'sendrecv'})
    pc.addTransceiver('video', {'direction': 'sendrecv'})
    
    const offer = await pc.createOffer(offerOption)
    await pc.setLocalDescription(offer)
    const id = uuidv4()

    console.log(pc.localDescription)

    socket.send(JSON.stringify({
        method: "join",
        params: { sid: "test room", offer: pc.localDescription },
        id: id
    }))

    socket.addEventListener('message', (event) => {
        const resp = JSON.parse(event.data)

        if (resp.id === id) {
            log(`Got publish answer`)

            pc.onnegotiationneeded = async function () {
                log("Renegotiating")
                const offer = await pc.createOffer()
                await pc.setLocalDescription(offer)
                const id = uuidv4()
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

window.subscribe = () => {
    join()
}

function uuidv4() {
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
        var r = Math.random() * 16 | 0, v = c == 'x' ? r : (r & 0x3 | 0x8);
        return v.toString(16);
    });
}