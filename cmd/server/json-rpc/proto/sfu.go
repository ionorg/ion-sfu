package proto

import "github.com/pion/webrtc/v3"

// Join message sent when initializing a peer connection
type Join struct {
	Sid    string                    `json:"sid"`
	Offer  webrtc.SessionDescription `json:"offer"`
	Codecs []string                  `json:"codecs"`
}

// Negotiation message sent when renegotiating
type Negotiation struct {
	Desc webrtc.SessionDescription `json:"desc"`
}

// Trickle message sent when renegotiating
type Trickle struct {
	Candidate webrtc.ICECandidateInit `json:"candidate"`
}
