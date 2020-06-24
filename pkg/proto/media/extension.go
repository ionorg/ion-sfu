package media

import (
	"encoding/json"
	fmt "fmt"
	"strings"
)

// BuildKey from media info
func (m Info) BuildKey() string {
	if m.Dc == "" {
		m.Dc = "*"
	}
	if m.Nid == "" {
		m.Nid = "*"
	}
	if m.Rid == "" {
		m.Rid = "*"
	}
	if m.Uid == "" {
		m.Uid = "*"
	}
	if m.Mid == "" {
		m.Mid = "*"
	}
	strs := []string{m.Dc, m.Nid, m.Rid, m.Uid, "media", "pub", m.Mid}
	return strings.Join(strs, "/")
}

// ParseInfoKey into info struct
// ex: dc1/sfu-tU2GInE5Lfuc/7485294b-9815-4888-83a5-631e77445b67/room1/media/pub/7e97c1e8-c80a-4c69-81b0-27efc83e6120
func ParseInfoKey(key string) (*Info, error) {
	var info Info
	arr := strings.Split(key, "/")
	if len(arr) != 7 {
		return nil, fmt.Errorf("Can‘t parse mediainfo; [%s]", key)
	}
	info.Dc = arr[0]
	info.Nid = arr[1]
	info.Rid = arr[2]
	info.Uid = arr[3]
	info.Mid = arr[6]
	return &info, nil
}

// MarshalTracks of a stream into a string
func (s Stream) MarshalTracks() (string, string, error) {
	str, err := json.Marshal(s.Tracks)
	if err != nil {
		return "track/" + s.Id, "", fmt.Errorf("Marshal: %v", err)
	}
	return "track/" + s.Id, string(str), nil
}

// UnmarshalTracks from string
func UnmarshalTracks(key string, value string) (*Stream, error) {
	var tracks []*Track
	if err := json.Unmarshal([]byte(value), &tracks); err != nil {
		return nil, fmt.Errorf("Unmarshal: %v", err)
	}
	if !strings.Contains(key, "track/") {
		return nil, fmt.Errorf("Invalid track failed => %s", key)
	}
	id := strings.Split(key, "/")[1]
	return &Stream{
		Id:     id,
		Tracks: tracks,
	}, nil
}
