package media

import (
	"fmt"
	"testing"
)

func TestMarshalTracks(t *testing.T) {
	stream := Stream{
		Id:     "msidxxxxxx",
		Tracks: []*Track{{Ssrc: 3694449886, Payload: 96, Type: "audio", Id: "aid"}},
	}
	key, value, err := stream.MarshalTracks()
	if err != nil {
		t.Error(err)
	}
	fmt.Printf("TrackField: key = %s => %s\n", key, value)
}

func TestUnMarshal(t *testing.T) {
	msid, tracks, err := UnmarshalTrackField("track/pion audio", `[{"ssrc": 3694449886, "pt": 111, "type": "audio", "id": "aid"}]`)
	if err != nil {
		t.Errorf("err => %v", err)
	}
	fmt.Printf("msid => %s, tracks => %v\n", msid, tracks)
}
