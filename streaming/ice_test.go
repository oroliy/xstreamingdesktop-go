package streaming

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeICE(t *testing.T) {
	in := []ICECandidate{{Candidate: "candidate:1 1 UDP 100 192.0.2.1 1234 typ host"}, {Candidate: "a=candidate:2 1 UDP 10 2001:0:4136:e378:8000:63bf:3fff:fdd2 4567 typ host"}, {Candidate: "a=end-of-candidates"}}
	got, err := NormalizeICE(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %v", got)
	}
	if !strings.Contains(got[0].Candidate, "2001:0:4136:e378:8000:63bf:3fff:fdd2") || !strings.Contains(got[0].Candidate, "2130706431") {
		t.Fatal(got[0])
	}
	if !strings.Contains(got[2].Candidate, "192.0.2.45 9002") || !strings.Contains(got[3].Candidate, "192.0.2.45 40000") {
		t.Fatal(got)
	}
	if got[4].Candidate != "a=end-of-candidates" {
		t.Fatal(got[4])
	}
}
func TestNormalizeICERejectsMalformed(t *testing.T) {
	for _, candidate := range []string{"bad", "candidate:1 1 UDP 1 no-ip 80 typ host", "candidate:1 1 UDP 1 1.2.3.4 0 typ host"} {
		if _, err := NormalizeICE([]ICECandidate{{Candidate: candidate}}, false); err == nil {
			t.Fatalf("accepted %q", candidate)
		}
	}
}

func TestICECandidateJSON(t *testing.T) {
	for _, raw := range []string{`{"candidate":"candidate:1","sdpMLineIndex":1,"sdpMid":"video"}`, `{"candidate":"candidate:1","sdpMLineIndex":"1","sdpMid":"video"}`} {
		var c ICECandidate
		if err := json.Unmarshal([]byte(raw), &c); err != nil || c.SDPMLineIndex != "1" {
			t.Fatal(c, err)
		}
		data, err := json.Marshal(c)
		if err != nil || !strings.Contains(string(data), `"sdpMLineIndex":1`) {
			t.Fatal(string(data), err)
		}
	}
}

func TestNormalizeICEPreservesTransportAndMedia(t *testing.T) {
	in := []ICECandidate{{Candidate: "candidate:abc 2 TCP 123 192.0.2.1 4444 typ relay tcptype passive raddr 192.0.2.5 rport 1234", SDPMid: "video", SDPMLineIndex: "2", MessageType: "iceCandidate"}}
	got, err := NormalizeICE(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SDPMid != "video" || got[0].SDPMLineIndex != "2" || got[0].Candidate != "a=candidate:abc 2 TCP 2130706431 192.0.2.1 4444 typ relay tcptype passive raddr 192.0.2.5 rport 1234" {
		t.Fatalf("candidate changed: %+v", got)
	}
}

func TestTeredoCandidatesRetainMedia(t *testing.T) {
	in := []ICECandidate{{Candidate: "candidate:2 1 UDP 10 2001:0:4136:e378:8000:63bf:3fff:fdd2 4567 typ host", SDPMid: "audio", SDPMLineIndex: "1"}}
	got, err := NormalizeICE(in, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range got[:len(got)-1] {
		if item.SDPMid != "audio" || item.SDPMLineIndex != "1" {
			t.Fatalf("lost media identity: %+v", item)
		}
	}
}

func TestNormalizeICEKeepsMissingMidForNonzeroLine(t *testing.T) {
	got, err := NormalizeICE([]ICECandidate{{Candidate: "candidate:1 1 UDP 10 192.0.2.1 4444 typ host", SDPMLineIndex: "2"}}, false)
	if err != nil || got[0].SDPMid != "" || got[0].SDPMLineIndex != "2" {
		t.Fatalf("candidate=%+v error=%v", got, err)
	}
}

func TestTeredoTCPDoesNotInventUDPCandidates(t *testing.T) {
	in := []ICECandidate{{Candidate: "candidate:2 1 TCP 10 2001:0:4136:e378:8000:63bf:3fff:fdd2 4567 typ host tcptype passive"}}
	got, err := NormalizeICE(in, false)
	if err != nil || len(got) != 2 || !strings.Contains(got[0].Candidate, " TCP ") {
		t.Fatalf("candidate=%+v error=%v", got, err)
	}
}

func TestNormalizeICEMDNSAndInvalidCandidates(t *testing.T) {
	input := []ICECandidate{
		{Candidate: "candidate:1 1 UDP 10 bad.example 1234 typ host"},
		{Candidate: "candidate:2 1 UDP 10 Console-123.LOCAL 2345 typ host", SDPMid: "video", SDPMLineIndex: "1"},
		{Candidate: "candidate:3 1 UDP 10 192.0.2.1 0 typ host"},
		{Candidate: "candidate:invalid bad UDP 10 192.0.2.4 1234 typ host"},
		{Candidate: "candidate:4 1 UDP 10 192.0.2.2 3456 typ host"},
	}
	got, err := NormalizeICE(input, false)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %+v, error %v", got, err)
	}
	if !strings.Contains(got[0].Candidate, "console-123.local 2345") || got[0].SDPMid != "video" || got[0].SDPMLineIndex != "1" || !strings.Contains(got[1].Candidate, "192.0.2.2") {
		t.Fatalf("valid candidates lost: %+v", got)
	}
	for _, name := range []string{".local", "-bad.local", "bad-.local", "a.b.local", "bad_.local", "host.example"} {
		_, err := NormalizeICE([]ICECandidate{{Candidate: "candidate:1 1 UDP 1 " + name + " 1234 typ host"}}, false)
		if err == nil {
			t.Errorf("accepted invalid hostname %q", name)
		}
	}
}
