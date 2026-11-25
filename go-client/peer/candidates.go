package peer

import (
	"strconv"
	"strings"

	"github.com/oroliy/xstreamingdesktop-go/go-client/streaming"
	"github.com/pion/sdp/v3"
)

// LocalCandidates extracts gathered candidates from the local SDP offer.
func LocalCandidates(offer string) ([]streaming.ICECandidate, error) {
	var desc sdp.SessionDescription
	if err := desc.Unmarshal([]byte(offer)); err != nil {
		return nil, err
	}
	var result []streaming.ICECandidate
	for index, media := range desc.MediaDescriptions {
		mid := ""
		for _, a := range media.Attributes {
			if a.Key == "mid" {
				mid = a.Value
				break
			}
		}
		for _, a := range media.Attributes {
			if a.Key == "candidate" {
				result = append(result, streaming.ICECandidate{Candidate: "candidate:" + strings.TrimPrefix(a.Value, "candidate:"), SDPMid: mid, SDPMLineIndex: strconv.Itoa(index)})
			}
		}
	}
	return result, nil
}
