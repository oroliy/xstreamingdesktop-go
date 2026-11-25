package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	appmedia "github.com/oroliy/xstreamingdesktop-go/go-client/media"
	"github.com/oroliy/xstreamingdesktop-go/go-client/protocol"
	"github.com/oroliy/xstreamingdesktop-go/go-client/streaming"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionNegotiationAndCleanup(t *testing.T) {
	virtualPulse := os.Getenv("XSTREAMING_TEST_PULSE_SERVER")
	if virtualPulse != "" {
		t.Setenv("PULSE_SERVER", virtualPulse)
	}
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	type microphonePacket struct {
		payload []byte
		track   *webrtc.TrackRemote
	}
	microphonePackets := make(chan microphonePacket, 1)
	remote.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		packet, _, err := track.ReadRTP()
		if err == nil {
			microphonePackets <- microphonePacket{payload: packet.Payload, track: track}
		}
	})
	var audioTrack *webrtc.TrackLocalStaticRTP
	audioReady := make(chan struct{}, 1)
	audioDone := make(chan struct{})
	audioErrors := make(chan error, 1)
	if _, err := exec.LookPath("ffplay"); err == nil {
		t.Setenv("SDL_AUDIODRIVER", "dummy")
		audioTrack, err = webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "stream")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = remote.AddTrack(audioTrack); err != nil {
			t.Fatal(err)
		}
	} else {
		if os.Getenv("XSTREAMING_REQUIRE_MEDIA_TESTS") == "1" {
			t.Fatalf("ffplay unavailable: %v", err)
		}
		t.Log("ffplay unavailable; audio integration not exercised")
	}
	inputPackets := make(chan []byte, 4)
	controlMessages := make(chan string, 4)
	remote.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			switch dc.Label() {
			case "message":
				var body struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(msg.Data, &body) == nil && body.Type == "Handshake" {
					if err := dc.SendText(`{"type":"HandshakeAck","version":"messageV1"}`); err != nil {
						t.Error(err)
					}
				}
			case "control":
				var body struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(msg.Data, &body) == nil {
					select {
					case controlMessages <- body.Message:
					default:
					}
				}
			case "input":
				select {
				case inputPackets <- append([]byte(nil), msg.Data...):
				default:
				}
			}
		})
	})
	var answer string
	var stopped atomic.Int32
	var chatOffers atomic.Int32
	var failChat atomic.Bool
	stoppedSignal := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/home/abc"}`)
		case r.URL.Path == "/v5/sessions/home/abc/state":
			fmt.Fprint(w, `{"state":"Provisioned"}`)
		case r.URL.Path == "/v5/sessions/home/abc/sdp" && r.Method == "POST":
			var offer struct {
				SDP           string `json:"sdp"`
				Configuration struct {
					Chat bool `json:"isMediaStreamsChatRenegotiation"`
				} `json:"configuration"`
			}
			if e := json.NewDecoder(r.Body).Decode(&offer); e != nil {
				t.Error(e)
				return
			}
			if offer.Configuration.Chat {
				chatOffers.Add(1)
				if failChat.Load() {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
			}
			if e := remote.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); e != nil {
				t.Error(e)
				return
			}
			a, e := remote.CreateAnswer(nil)
			if e != nil {
				t.Error(e)
				return
			}
			done := webrtc.GatheringCompletePromise(remote)
			if e = remote.SetLocalDescription(a); e != nil {
				t.Error(e)
				return
			}
			<-done
			answer = remote.LocalDescription().SDP
			w.WriteHeader(204)
		case r.URL.Path == "/v5/sessions/home/abc/sdp":
			b, _ := json.Marshal(map[string]string{"sdp": answer})
			json.NewEncoder(w).Encode(map[string]string{"exchangeResponse": string(b)})
		case r.URL.Path == "/v5/sessions/home/abc/ice" && r.Method == "POST":
			w.WriteHeader(204)
		case r.URL.Path == "/v5/sessions/home/abc/ice":
			fmt.Fprint(w, `{"exchangeResponse":"[]"}`)
		case r.URL.Path == "/v5/sessions/home/abc" && r.Method == "DELETE":
			stopped.Add(1)
			select {
			case stoppedSignal <- struct{}{}:
			default:
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connected := make(chan struct{}, 1)
	s, err := Start(ctx, client, Options{Play: streaming.PlayOptions{Target: "console"}, OnTrack: func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		defer close(audioDone)
		player, err := appmedia.NewAudioPlayer(ctx, 2, 100)
		if err != nil {
			audioErrors <- err
			return
		}
		defer player.Close()
		packet, _, err := track.ReadRTP()
		if err == nil {
			err = player.WriteRTP(packet)
		}
		if err != nil {
			audioErrors <- err
			return
		}
		audioReady <- struct{}{}
		_ = appmedia.ReceiveOpus(track, player)
	}, OnConnectionState: func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("connection state did not reach connected")
	}
	if audioTrack != nil {
		audioReceived := false
		for i := 0; i < 100 && !audioReceived; i++ {
			packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: []byte{0xf8, 0xff, 0xfe}}
			if err := audioTrack.WriteRTP(packet); err != nil {
				t.Fatal(err)
			}
			select {
			case <-audioReady:
				audioReceived = true
			case err := <-audioErrors:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("audio track did not start")
			case <-time.After(10 * time.Millisecond):
			}
		}
		if !audioReceived {
			t.Fatal("audio packet did not reach player")
		}
	}
	select {
	case <-s.Ready():
	case <-ctx.Done():
		t.Fatal("control handshake did not complete")
	}
	for _, want := range []string{"authorizationRequest", "gamepadChanged"} {
		select {
		case got := <-controlMessages:
			if got != want {
				t.Fatalf("control message %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal("missing control message")
		}
	}
	sender := s.Input()
	for s.InputChannel().ReadyState() != webrtc.DataChannelStateOpen {
		select {
		case <-ctx.Done():
			t.Fatal("input channel did not open")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := sender.Init(0); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendGamepads([]protocol.Gamepad{{Buttons: protocol.A, LeftX: 1}}); err != nil {
		t.Fatal(err)
	}
	for _, reportType := range []uint16{8, 2} {
		select {
		case packet := <-inputPackets:
			if len(packet) < 15 || binary.LittleEndian.Uint16(packet) != reportType {
				t.Fatalf("unexpected input report %x", packet)
			}
			if reportType == 2 && (len(packet) != 38 || binary.LittleEndian.Uint16(packet[16:]) != uint16(protocol.A)) {
				t.Fatalf("invalid gamepad report %x", packet)
			}
		case <-ctx.Done():
			t.Fatal("missing input report")
		}
	}
	mic, err := s.StartMicrophone(ctx)
	if err != nil {
		t.Fatalf("negotiate microphone: %v", err)
	}
	if chatOffers.Load() != 1 {
		t.Fatal("missing chat renegotiation")
	}
	if _, err = s.StartMicrophone(ctx); err == nil {
		t.Fatal("accepted a second active microphone")
	}
	sent := []byte{0xf8, 0xff, 0xfe}
	if _, lookupErr := exec.LookPath("ffmpeg"); lookupErr == nil {
		oneSample := errors.New("one sample sent")
		input := []string{"-re", "-f", "lavfi", "-i", "sine=frequency=440:duration=30"}
		if virtualPulse != "" {
			input = []string{"-f", "pulse", "-i", "default"}
		}
		encodeErr := appmedia.EncodeMicrophone(ctx, input, func(sample pionmedia.Sample) error {
			sent = append([]byte(nil), sample.Data...)
			if err := mic.WriteSample(sample); err != nil {
				return err
			}
			return oneSample
		})
		if !errors.Is(encodeErr, oneSample) {
			if os.Getenv("XSTREAMING_REQUIRE_MEDIA_TESTS") == "1" || virtualPulse != "" {
				t.Fatalf("encode microphone audio: %v", encodeErr)
			}
			t.Logf("Opus encoder unavailable; using fixed packet: %v", encodeErr)
			sent = []byte{0xf8, 0xff, 0xfe}
			if err = mic.WriteSample(pionmedia.Sample{Data: sent, Duration: 20 * time.Millisecond}); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if os.Getenv("XSTREAMING_REQUIRE_MEDIA_TESTS") == "1" || virtualPulse != "" {
			t.Fatalf("ffmpeg unavailable: %v", lookupErr)
		}
		if err = mic.WriteSample(pionmedia.Sample{Data: sent, Duration: 20 * time.Millisecond}); err != nil {
			t.Fatal(err)
		}
	}
	var remoteMic *webrtc.TrackRemote
	select {
	case packet := <-microphonePackets:
		if !bytes.Equal(packet.payload, sent) {
			t.Fatalf("unexpected microphone packet: %x", packet.payload)
		}
		remoteMic = packet.track
	case <-ctx.Done():
		t.Fatal("microphone packet did not arrive")
	}
	if err = s.StopMicrophone(); err != nil {
		t.Fatal(err)
	}
	if err = s.StopMicrophone(); err != nil {
		t.Fatal(err)
	}
	if err = remoteMic.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err = mic.WriteSample(pionmedia.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err == nil {
		t.Fatal("stopped microphone accepted a sample")
	}
	if _, _, err = remoteMic.ReadRTP(); err == nil {
		t.Fatal("microphone sent data after stop")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("unexpected read error after stop: %v", err)
	}
	secondMic, err := s.StartMicrophone(ctx)
	if err != nil {
		t.Fatalf("restart microphone: %v", err)
	}
	if chatOffers.Load() != 2 {
		t.Fatalf("restart did not renegotiate: %d offers", chatOffers.Load())
	}
	if err := mic.WriteSample(pionmedia.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err == nil {
		t.Fatal("previous microphone handle became active again")
	}
	if err := remoteMic.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := secondMic.WriteSample(pionmedia.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	readOld := make(chan error, 1)
	go func() {
		packet, _, err := remoteMic.ReadRTP()
		if err == nil && !bytes.Equal(packet.Payload, []byte{0xf8, 0xff, 0xfe}) {
			err = fmt.Errorf("unexpected restarted packet: %x", packet.Payload)
		}
		readOld <- err
	}()
	select {
	case packet := <-microphonePackets:
		if !bytes.Equal(packet.payload, []byte{0xf8, 0xff, 0xfe}) {
			t.Fatalf("unexpected new track packet: %x", packet.payload)
		}
	case err := <-readOld:
		if err != nil {
			t.Fatalf("restarted microphone did not send: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("restarted microphone timed out")
	}
	if err := s.StopMicrophone(); err != nil {
		t.Fatal(err)
	}
	failChat.Store(true)
	if _, err = s.StartMicrophone(ctx); err == nil {
		t.Fatal("accepted failed chat renegotiation")
	}
	select {
	case <-stoppedSignal:
	case <-time.After(3 * time.Second):
		t.Fatal("session not stopped after context cancellation")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if audioTrack != nil {
		select {
		case <-audioDone:
		case <-time.After(3 * time.Second):
			t.Fatal("audio track did not stop")
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if stopped.Load() != 1 {
		t.Fatal("session not stopped exactly once")
	}
}

// A provisioning failure must release the remote session even if the caller's context is canceled.
func TestFailedProvisioningCleansUp(t *testing.T) {
	var stopped atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/failed"}`)
		case r.URL.Path == "/v5/sessions/home/failed/state":
			fmt.Fprint(w, `{"state":"Failed","errorDetails":{"code":"no-capacity"}}`)
		case r.URL.Path == "/v5/sessions/home/failed" && r.Method == http.MethodDelete:
			stopped.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), client, Options{Play: streaming.PlayOptions{Target: "console"}})
	if err == nil || s != nil {
		t.Fatalf("unexpected session or error: %v", err)
	}
	if stopped.Load() != 1 {
		t.Fatalf("stopped %d times", stopped.Load())
	}
}
func TestCanceledProvisioningCleansUp(t *testing.T) {
	var stopped atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/canceled"}`)
		case r.URL.Path == "/v5/sessions/home/canceled/state":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v5/sessions/home/canceled" && r.Method == http.MethodDelete:
			stopped.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s, err := Start(ctx, client, Options{Play: streaming.PlayOptions{Target: "console"}})
	if s != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected result: %v", err)
	}
	if stopped.Load() != 1 {
		t.Fatalf("stopped %d times", stopped.Load())
	}
}

func TestSignalingFailureCleansUp(t *testing.T) {
	var stopped atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/signaling"}`)
		case r.URL.Path == "/v5/sessions/home/signaling/state":
			fmt.Fprint(w, `{"state":"Provisioned"}`)
		case r.URL.Path == "/v5/sessions/home/signaling/sdp" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusBadGateway)
		case r.URL.Path == "/v5/sessions/home/signaling" && r.Method == http.MethodDelete:
			stopped.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Start(ctx, client, Options{Play: streaming.PlayOptions{Target: "console"}})
	var response *streaming.HTTPError
	if s != nil || !errors.As(err, &response) || response.Status != http.StatusBadGateway {
		t.Fatalf("unexpected result: %v", err)
	}
	if stopped.Load() != 1 {
		t.Fatalf("stopped %d times", stopped.Load())
	}
}

func TestStartupErrorPreservesCleanupFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/failed"}`)
		case r.URL.Path == "/v5/sessions/home/failed/state":
			fmt.Fprint(w, `{"state":"Failed","errorDetails":{"code":"no-capacity"}}`)
		case r.URL.Path == "/v5/sessions/home/failed" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusBadGateway)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Start(context.Background(), client, Options{Play: streaming.PlayOptions{Target: "console"}})
	var cleanup *CleanupError
	var httpErr *streaming.HTTPError
	if err == nil || !strings.Contains(err.Error(), "no-capacity") || !errors.As(err, &cleanup) || !errors.As(cleanup, &httpErr) || httpErr.Status != http.StatusBadGateway {
		t.Fatalf("expected provisioning and cleanup errors, got %v", err)
	}
}

func TestConnectionTimeoutAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go monitorConnection(ctx, make(chan struct{}), 10*time.Millisecond, cancel)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrConnectionTimeout) {
			t.Fatal(context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("connection did not time out")
	}
}

func TestHandshakeTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	connected := make(chan struct{})
	go monitorHandshake(ctx, connected, make(chan struct{}), 20*time.Millisecond, cancel)
	// The connection deadline is handled separately; do not time out early.
	time.Sleep(40 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("handshake timed out before connection")
	}
	close(connected)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrHandshakeTimeout) {
			t.Fatal(context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("handshake did not time out")
	}
}

func TestReadyHandshakeDoesNotTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	connected, ready, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	close(connected)
	close(ready)
	go func() { monitorHandshake(ctx, connected, ready, time.Millisecond, cancel); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handshake monitor did not stop")
	}
	if err := context.Cause(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectWatchRecoveryAndTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	watch := &disconnectWatch{timeout: 40 * time.Millisecond, cancel: cancel}
	defer watch.stop()
	// The initial connection has its own timeout.
	watch.update(webrtc.PeerConnectionStateDisconnected)
	time.Sleep(60 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("initial disconnection ended the session")
	}
	watch.update(webrtc.PeerConnectionStateConnected)
	watch.update(webrtc.PeerConnectionStateDisconnected)
	watch.update(webrtc.PeerConnectionStateConnected)
	time.Sleep(60 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("recovered connection ended the session")
	}
	watch.update(webrtc.PeerConnectionStateDisconnected)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrDisconnectTimeout) {
			t.Fatal(context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("persistent disconnection did not end the session")
	}
}

func TestDisconnectWatchStopsTimer(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	watch := &disconnectWatch{timeout: 20 * time.Millisecond, cancel: cancel}
	watch.update(webrtc.PeerConnectionStateConnected)
	watch.update(webrtc.PeerConnectionStateDisconnected)
	watch.stop()
	time.Sleep(40 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("stopped watcher canceled session")
	}
}

func TestConnectedSessionIsNotTimedOut(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	connected := make(chan struct{})
	close(connected)
	done := make(chan struct{})
	go func() { monitorConnection(ctx, connected, time.Millisecond, cancel); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after connection")
	}
	if err := context.Cause(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionTimeoutStopsRemoteSession(t *testing.T) {
	testStartupTimeoutStopsRemoteSession(t, false)
}

func TestHandshakeTimeoutStopsRemoteSession(t *testing.T) {
	testStartupTimeoutStopsRemoteSession(t, true)
}

// The remote either has no ICE path or connects without acknowledging the
// message handshake. Both failures must stop the provisioned remote session.
func testStartupTimeoutStopsRemoteSession(t *testing.T, allowConnection bool) {
	t.Helper()
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	withoutCandidates := func(sdp string) string {
		if allowConnection {
			return sdp
		}
		lines := strings.Split(sdp, "\r\n")
		filtered := lines[:0]
		for _, line := range lines {
			if !strings.HasPrefix(line, "a=candidate:") && line != "a=end-of-candidates" {
				filtered = append(filtered, line)
			}
		}
		return strings.Join(filtered, "\r\n")
	}
	var answer string
	var stops atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/timeout"}`)
		case r.URL.Path == "/v5/sessions/home/timeout/state":
			fmt.Fprint(w, `{"state":"Provisioned"}`)
		case r.URL.Path == "/v5/sessions/home/timeout/sdp" && r.Method == http.MethodPost:
			var offer struct {
				SDP string `json:"sdp"`
			}
			if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := remote.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: withoutCandidates(offer.SDP)}); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			desc, err := remote.CreateAnswer(nil)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			gathered := webrtc.GatheringCompletePromise(remote)
			if err := remote.SetLocalDescription(desc); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			<-gathered
			answer = withoutCandidates(remote.LocalDescription().SDP)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v5/sessions/home/timeout/sdp":
			payload, _ := json.Marshal(map[string]string{"sdp": answer})
			_ = json.NewEncoder(w).Encode(map[string]string{"exchangeResponse": string(payload)})
		case r.URL.Path == "/v5/sessions/home/timeout/ice" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v5/sessions/home/timeout/ice":
			fmt.Fprint(w, `{"exchangeResponse":"[]"}`)
		case r.URL.Path == "/v5/sessions/home/timeout" && r.Method == http.MethodDelete:
			stops.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Start(ctx, client, Options{Play: streaming.PlayOptions{Target: "console"}, ConnectionTimeout: time.Second, HandshakeTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case <-s.Done():
		want := ErrConnectionTimeout
		if allowConnection {
			want = ErrHandshakeTimeout
		}
		if !errors.Is(s.Err(), want) {
			t.Fatalf("unexpected session cause: %v", s.Err())
		}
	case <-ctx.Done():
		t.Fatal("session did not time out")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 1 {
		t.Fatalf("remote session stopped %d times", stops.Load())
	}
}

func TestSignalingTimeoutCleansUp(t *testing.T) {
	var stops atomic.Int32
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/home/play":
			fmt.Fprint(w, `{"sessionPath":"/v5/sessions/slow"}`)
		case r.URL.Path == "/v5/sessions/home/slow/state":
			fmt.Fprint(w, `{"state":"Provisioned"}`)
		case r.URL.Path == "/v5/sessions/home/slow/sdp":
			if r.Method == http.MethodGet {
				polls.Add(1)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v5/sessions/home/slow" && r.Method == http.MethodDelete:
			stops.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := streaming.NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Start(ctx, client, Options{Play: streaming.PlayOptions{Target: "console"}, SignalingTimeout: 500 * time.Millisecond})
	if s != nil || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("expected signaling timeout, got session %v and error %v", s, err)
	}
	if stops.Load() != 1 || polls.Load() == 0 {
		t.Fatalf("stops=%d signaling polls=%d", stops.Load(), polls.Load())
	}
}

func TestRemoteCandidatesTryAllPaths(t *testing.T) {
	failure := errors.New("candidate rejected")
	candidates := []streaming.ICECandidate{
		{Candidate: "bad", SDPMLineIndex: "0"},
		{Candidate: "good", SDPMid: "video", SDPMLineIndex: "2"},
		{Candidate: "a=end-of-candidates", SDPMLineIndex: "0"},
	}
	var attempted []string
	err := addRemoteCandidates(candidates, func(candidate, mid string, line uint16) error {
		attempted = append(attempted, candidate)
		if candidate == "bad" {
			return failure
		}
		if mid != "video" || line != 2 {
			t.Fatalf("lost media identity: %s %d", mid, line)
		}
		return nil
	})
	if err != nil || len(attempted) != 2 || attempted[1] != "good" {
		t.Fatalf("attempted=%v error=%v", attempted, err)
	}
	err = addRemoteCandidates(candidates[:1], func(string, string, uint16) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("lost failure: %v", err)
	}
	if err = addRemoteCandidates(nil, func(string, string, uint16) error { t.Fatal("unexpected candidate"); return nil }); err != nil {
		t.Fatalf("empty exchange rejected: %v", err)
	}
}
