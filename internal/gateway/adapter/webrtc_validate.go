package adapter

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

func validWebRTCMode(mode string) bool {
	switch mode {
	case "reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered":
		return true
	default:
		return false
	}
}

func validWebRTCSDP(raw string) bool {
	if !utf8.ValidString(raw) || len(raw) == 0 || len(raw) > maxWebRTCSDPBytes || strings.Contains(raw, "\x00") {
		return false
	}
	media := 0
	candidates := 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "m=") {
			media++
			fields := strings.Fields(line)
			if len(fields) != 4 || fields[0] != "m=application" || fields[2] != "UDP/DTLS/SCTP" || fields[3] != "webrtc-datachannel" {
				return false
			}
			port, err := strconv.Atoi(fields[1])
			if err != nil || port < 1 || port > 65535 {
				return false
			}
		}
		if strings.HasPrefix(line, "a=candidate:") {
			candidates++
			if len(line) > 2048 {
				return false
			}
		}
	}
	return media == 1 && candidates <= 128
}
