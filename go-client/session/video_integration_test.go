package session

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	appmedia "github.com/oroliy/xstreamingdesktop-go/go-client/media"
	"github.com/oroliy/xstreamingdesktop-go/go-client/streaming"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

// Exercises signaling, real local WebRTC packets, H.264 depacketization and decoding.
func TestVideoThroughSession(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	video, err := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=s=128x128:r=15:d=1", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-f", "h264", "pipe:1").Output()
	if err != nil {
		mediaTestUnavailable(t, "H.264 encoder unavailable: %v", err)
	}
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"}, "video", "stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	var answer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/test"}`)
		case r.URL.Path == "/v5/sessions/home/test/state":
			fmt.Fprint(w, `{"state":"Provisioned"}`)
		case r.URL.Path == "/v5/sessions/home/test/sdp" && r.Method == "POST":
			var offer struct {
				SDP string `json:"sdp"`
			}
			if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
				t.Error(err)
				return
			}
			if err := remote.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
				t.Error(err)
				return
			}
			a, err := remote.CreateAnswer(nil)
			if err != nil {
				t.Error(err)
				return
			}
			done := webrtc.GatheringCompletePromise(remote)
			if err = remote.SetLocalDescription(a); err != nil {
				t.Error(err)
				return
			}
			<-done
			answer = remote.LocalDescription().SDP
			w.WriteHeader(204)
		case r.URL.Path == "/v5/sessions/home/test/sdp":
			b, _ := json.Marshal(map[string]string{"sdp": answer})
			_ = json.NewEncoder(w).Encode(map[string]string{"exchangeResponse": string(b)})
		case r.URL.Path == "/v5/sessions/home/test/ice" && r.Method == "POST":
			w.WriteHeader(204)
		case r.URL.Path == "/v5/sessions/home/test/ice":
			fmt.Fprint(w, `{"exchangeResponse":"[]"}`)
		case r.URL.Path == "/v5/sessions/home/test" && r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	client, err := streaming.NewClient(srv.URL, "token", "home", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan image.Image, 1)
	connected := make(chan struct{}, 1)
	trackDone := make(chan struct{})
	trackErr := make(chan error, 1)
	s, err := Start(ctx, client, Options{Play: streaming.PlayOptions{Target: "console"}, OnConnectionState: func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}, OnTrack: func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		defer close(trackDone)
		decoder, e := appmedia.NewVideoDecoder(ctx, func(img image.Image) {
			select {
			case frames <- img:
			default:
			}
		})
		if e != nil {
			trackErr <- e
			return
		}
		defer decoder.Close()
		if e = appmedia.ReceiveH264(track, decoder); e != nil && ctx.Err() == nil {
			trackErr <- e
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 20; i++ {
		if err = track.WriteSample(pionmedia.Sample{Data: video, Duration: time.Second}); err != nil {
			t.Fatal(err)
		}
		select {
		case img := <-frames:
			if img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
				t.Fatal(img.Bounds())
			}
			cancel()
			_ = s.Close()
			select {
			case <-trackDone:
			case <-time.After(3 * time.Second):
				t.Fatal("track did not stop")
			}
			return
		case err = <-trackErr:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	cancel()
	_ = s.Close()
	t.Fatal("no decoded frame")
}

func mediaTestUnavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("XSTREAMING_REQUIRE_MEDIA_TESTS") == "1" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}
