package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/media"
	"github.com/oroliy/xstreamingdesktop-go/session"
	"github.com/oroliy/xstreamingdesktop-go/settings"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

type testMicrophoneSession struct {
	start func(context.Context) (microphoneWriter, error)
	stops int
}

func TestMicrophoneControlVisibility(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{prefs: settings.Defaults(), streamCancel: cancel, streamCtx: ctx, micSession: &session.Session{}}
	tt := ui.NewTester(a.view, 1024, 768)
	if tt.HasText("Enable microphone") {
		t.Fatal("microphone offered before connection")
	}
	a.streamConnected = true
	tt.Frame()
	if got := tt.HasText("Enable microphone"); got != microphoneSupported(runtime.GOOS) {
		t.Fatalf("unexpected microphone visibility: %v", got)
	}
	a.controlsHidden = true
	a.micStatus = "Microphone on."
	tt.Frame()
	if !tt.HasText("Microphone on.") {
		t.Fatal("capture status hidden with controls")
	}
}

func TestMicrophoneStopButtonCancelsCapture(t *testing.T) {
	if !microphoneSupported(runtime.GOOS) {
		t.Skip("microphone control is unavailable")
	}
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	micCtx, stopMic := context.WithCancel(streamCtx)
	defer stopMic()
	a := &app{prefs: settings.Defaults(), streamCancel: stopStream, streamCtx: streamCtx, streamConnected: true, micSession: &session.Session{}, micCtx: micCtx, micCancel: stopMic}
	tt := ui.NewTester(a.view, 1024, 768)
	if err := tt.Click("Disable microphone"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(micCtx.Err(), context.Canceled) || !a.micStopping || a.micStatus != "Stopping microphone…" {
		t.Fatal("stop did not cancel capture")
	}
}

func (s *testMicrophoneSession) StartMicrophone(ctx context.Context) (microphoneWriter, error) {
	return s.start(ctx)
}
func (s *testMicrophoneSession) StopMicrophone() error { s.stops++; return nil }

func testMicrophoneTrack(t *testing.T) *webrtc.TrackLocalStaticSample {
	t.Helper()
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", "test")
	if err != nil {
		t.Fatal(err)
	}
	return track
}

func TestMicrophoneInputArgs(t *testing.T) {
	for _, tc := range []struct {
		platform string
		source   string
		want     []string
	}{
		{"linux", "", []string{"-f", "pulse", "-i", "default"}},
		{"linux", "alsa_input.example", []string{"-f", "pulse", "-i", "alsa_input.example"}},
		{"darwin", "ignored", []string{"-f", "avfoundation", "-i", ":default"}},
		{"windows", "@device_cm_{abc}\\wave_{123}", []string{"-f", "dshow", "-i", "audio=@device_cm_{abc}\\wave_{123}"}},
	} {
		got, err := microphoneInputArgs(tc.platform, tc.source)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("platform=%s got=%v error=%v", tc.platform, got, err)
		}
	}
	if _, err := microphoneInputArgs("windows", "default"); !errors.Is(err, errWindowsMicrophoneNotSelected) {
		t.Fatalf("accepted missing Windows source: %v", err)
	}
	if _, err := microphoneInputArgs("windows", "audio:video"); err == nil {
		t.Fatal("accepted an ambiguous DirectShow source")
	}
	if _, err := microphoneInputArgs("plan9", "default"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
}

func TestUnsupportedMicrophoneDoesNotNegotiate(t *testing.T) {
	s := &testMicrophoneSession{start: func(context.Context) (microphoneWriter, error) {
		t.Fatal("unsupported platform negotiated audio")
		return nil, nil
	}}
	err, stopErr := runMicrophone(context.Background(), context.Background(), "plan9", "default", s, func(context.Context, []string, func(pionmedia.Sample) error) error {
		t.Fatal("unsupported platform started capture")
		return nil
	}, func() {})
	if err == nil || stopErr != nil || s.stops != 0 {
		t.Fatalf("error=%v stop=%v calls=%d", err, stopErr, s.stops)
	}
}

func TestUnselectedWindowsMicrophoneDoesNotNegotiate(t *testing.T) {
	s := &testMicrophoneSession{start: func(context.Context) (microphoneWriter, error) {
		t.Fatal("unselected microphone negotiated audio")
		return nil, nil
	}}
	err, stopErr := runMicrophone(context.Background(), context.Background(), "windows", "default", s, nil, func() {})
	if !errors.Is(err, errWindowsMicrophoneNotSelected) || stopErr != nil || s.stops != 0 {
		t.Fatalf("error=%v stop=%v calls=%d", err, stopErr, s.stops)
	}
}

func TestWindowsMicrophoneUsesChosenDevice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &testMicrophoneSession{start: func(context.Context) (microphoneWriter, error) { return testMicrophoneTrack(t), nil }}
	err, stopErr := runMicrophone(ctx, ctx, "windows", "Microphone (USB)", s, func(ctx context.Context, args []string, send func(pionmedia.Sample) error) error {
		if !reflect.DeepEqual(args, []string{"-f", "dshow", "-i", "audio=Microphone (USB)"}) {
			t.Fatalf("unexpected DirectShow arguments: %v", args)
		}
		if err := send(pionmedia.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		cancel()
		return ctx.Err()
	}, func() {})
	if !errors.Is(err, context.Canceled) || stopErr != nil || s.stops != 1 {
		t.Fatalf("error=%v stop=%v detach=%d", err, stopErr, s.stops)
	}
}

func TestMicrophoneCaptureStopsAndDetaches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &testMicrophoneSession{start: func(context.Context) (microphoneWriter, error) { return testMicrophoneTrack(t), nil }}
	active := 0
	source := "alsa_input.usb-example"
	err, stopErr := runMicrophone(ctx, ctx, "linux", source, s, func(ctx context.Context, args []string, send func(pionmedia.Sample) error) error {
		if !reflect.DeepEqual(args, []string{"-f", "pulse", "-i", source}) {
			t.Fatal(args)
		}
		for i := 0; i < 2; i++ {
			if err := send(pionmedia.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		return ctx.Err()
	}, func() { active++ })
	if !errors.Is(err, context.Canceled) || stopErr != nil || active != 1 || s.stops != 1 {
		t.Fatalf("err=%v stop=%v active=%d detach=%d", err, stopErr, active, s.stops)
	}
}

func TestCancelDuringMicrophoneNegotiationSkipsCapture(t *testing.T) {
	micCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	track := testMicrophoneTrack(t)
	s := &testMicrophoneSession{start: func(context.Context) (microphoneWriter, error) {
		cancel()
		return track, nil
	}}
	err, stopErr := runMicrophone(context.Background(), micCtx, "linux", "default", s, func(context.Context, []string, func(pionmedia.Sample) error) error {
		t.Fatal("capture started after cancellation")
		return nil
	}, func() { t.Fatal("announced active after cancellation") })
	if !errors.Is(err, context.Canceled) || stopErr != nil || s.stops != 1 {
		t.Fatalf("err=%v stop=%v detach=%d", err, stopErr, s.stops)
	}
}

func TestSaveMicrophoneSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{prefs: settings.Defaults(), settingsPath: path, micSourceDraft: "  alsa_input.usb-example  "}
	a.saveMicrophoneSource()
	saved, err := settings.Load(path)
	if err != nil || saved.MicrophoneSource != "alsa_input.usb-example" || a.prefs.MicrophoneSource != saved.MicrophoneSource {
		t.Fatalf("settings=%+v error=%v", saved, err)
	}
	a.micSourceDraft = "  "
	a.saveMicrophoneSource()
	if a.prefs.MicrophoneSource != "default" {
		t.Fatal("empty input did not restore the default source")
	}
	a.micSourceDraft = "bad\x00source"
	a.saveMicrophoneSource()
	if a.prefs.MicrophoneSource != "default" || a.status != "Invalid microphone input name." {
		t.Fatal("invalid input replaced the saved source")
	}
}

func TestMicrophoneSourceSettingsUI(t *testing.T) {
	if !microphoneSourceSettingsSupported(runtime.GOOS) {
		t.Skip("microphone input setting is unavailable")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{prefs: settings.Defaults(), settingsPath: path}
	tt := ui.NewTester(a.view, 1024, 768)
	label, source := "PulseAudio input name", "alsa_input.example"
	if runtime.GOOS == "windows" {
		label, source = "DirectShow input name", "Microphone (USB)"
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Type(source)
	if err := tt.Click("Save microphone input"); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.Load(path)
	if err != nil || saved.MicrophoneSource != source {
		t.Fatalf("source=%q error=%v", saved.MicrophoneSource, err)
	}
}

func TestMicrophoneSourceOptionsPreserveSelection(t *testing.T) {
	got, names, selected := microphoneSourceOptions([]media.PulseSource{{Name: "default"}, {Name: "virtual_mic", Description: "Virtual mic"}, {Name: "virtual_mic"}}, "disconnected_mic")
	want := []string{"default", "Virtual mic (virtual_mic)", "disconnected_mic"}
	if !reflect.DeepEqual(got, want) || names["Virtual mic (virtual_mic)"] != "virtual_mic" || selected != "disconnected_mic" {
		t.Fatalf("got %v, names=%v, selected=%q", got, names, selected)
	}
}

func TestChooseDiscoveredMicrophoneSource(t *testing.T) {
	if !microphoneSourceSettingsSupported(runtime.GOOS) {
		t.Skip("microphone input setting is unavailable")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{prefs: settings.Defaults(), settingsPath: path, micSourceDraft: "default", micSources: []media.PulseSource{{Name: "virtual_mic", Description: "Virtual mic"}}}
	tt := ui.NewTester(a.view, 1024, 768)
	if err := tt.Click("Available microphone inputs"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Virtual mic (virtual_mic)"); err != nil {
		t.Fatalf("%v; visible: %v", err, tt.Texts())
	}
	if err := tt.Click("Save microphone input"); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.Load(path)
	if err != nil || saved.MicrophoneSource != "virtual_mic" {
		t.Fatalf("source=%q error=%v", saved.MicrophoneSource, err)
	}
}

func TestMicrophoneDiscoveryFailureKeepsManualChoice(t *testing.T) {
	if !microphoneSourceSettingsSupported(runtime.GOOS) {
		t.Skip("microphone input setting is unavailable")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{prefs: settings.Defaults(), settingsPath: path, micSourceDraft: "disconnected_mic", micSourcesLoading: true, micSources: []media.PulseSource{{Name: "previous_mic"}}}
	a.finishMicrophoneSources(nil, errors.New("service unavailable"))
	if a.micSourcesLoading || a.micSourceDraft != "disconnected_mic" || !reflect.DeepEqual(a.micSources, []media.PulseSource{{Name: "previous_mic"}}) {
		t.Fatal("failed discovery changed the existing selection")
	}
	tt := ui.NewTester(a.view, 1024, 768)
	if !tt.HasText("Unable to list microphone inputs: service unavailable") {
		t.Fatal("discovery failure was not shown")
	}
	if err := tt.Click("Save microphone input"); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.Load(path)
	if err != nil || saved.MicrophoneSource != "disconnected_mic" {
		t.Fatalf("manual source=%q error=%v", saved.MicrophoneSource, err)
	}
}

func TestMicrophoneDiscoveryUpdatesDisplayWithoutChangingSource(t *testing.T) {
	a := &app{micSourceDraft: "virtual_mic", micSourceChoice: "virtual_mic"}
	a.finishMicrophoneSources([]media.PulseSource{{Name: "virtual_mic", Description: "Virtual microphone"}}, nil)
	if a.micSourceDraft != "virtual_mic" || a.micSourceChoice != "Virtual microphone (virtual_mic)" {
		t.Fatalf("draft=%q display=%q", a.micSourceDraft, a.micSourceChoice)
	}
	a.finishMicrophoneSources(nil, nil)
	if a.micSourceDraft != "virtual_mic" || a.micSourceChoice != "virtual_mic" {
		t.Fatalf("disconnected source lost: draft=%q display=%q", a.micSourceDraft, a.micSourceChoice)
	}
}
