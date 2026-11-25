package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/media"
	"github.com/oroliy/xstreamingdesktop-go/session"
	"github.com/oroliy/xstreamingdesktop-go/settings"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

type microphoneWriter interface {
	WriteSample(pionmedia.Sample) error
}

type microphoneSession interface {
	StartMicrophone(context.Context) (microphoneWriter, error)
	StopMicrophone() error
}

type liveMicrophoneSession struct{ *session.Session }

func (s liveMicrophoneSession) StartMicrophone(ctx context.Context) (microphoneWriter, error) {
	return s.Session.StartMicrophone(ctx)
}

type microphoneEncoder func(context.Context, []string, func(pionmedia.Sample) error) error

var errWindowsMicrophoneNotSelected = errors.New("Choose a Windows microphone in settings.")

func microphoneSourceOptions(sources []media.AudioSource, selected string) ([]string, map[string]string, string) {
	options := []string{"default"}
	seen := map[string]bool{"default": true}
	names := map[string]string{"default": "default"}
	choice := "default"
	for _, source := range sources {
		if source.Name != "" && !seen[source.Name] {
			seen[source.Name] = true
			label := source.Name
			if source.Description != "" && source.Description != source.Name {
				label = source.Description + " (" + source.Name + ")"
			}
			options = append(options, label)
			names[label] = source.Name
			if selected == source.Name {
				choice = label
			}
		}
	}
	if selected != "" && !seen[selected] {
		choice = selected
		if _, exists := names[choice]; exists {
			choice += " (manual)"
		}
		options = append(options, choice)
		names[choice] = selected
	}
	return options, names, choice
}

func (a *app) refreshMicrophoneSources() {
	if a.micSourcesLoading || a.ctx == nil || a.ctx.Err() != nil {
		return
	}
	a.micSourcesLoading = true
	a.micSourcesStatus = "Loading microphone inputs…"
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		var sources []media.AudioSource
		var err error
		if runtime.GOOS == "windows" {
			sources, err = media.DirectShowSources(ctx)
		} else {
			sources, err = media.PulseSources(ctx)
		}
		a.update(func() {
			if a.ctx.Err() != nil {
				return
			}
			a.finishMicrophoneSources(sources, err)
		})
	}()
}

func (a *app) finishMicrophoneSources(sources []media.AudioSource, err error) {
	a.micSourcesLoading = false
	if err != nil {
		a.micSourcesStatus = "Unable to list microphone inputs: " + err.Error()
		return
	}
	a.micSources = sources
	_, _, a.micSourceChoice = microphoneSourceOptions(a.micSources, a.micSourceDraft)
	a.micSourcesStatus = "Microphone inputs refreshed."
	if len(sources) == 0 {
		a.micSourcesStatus = "No microphone inputs found."
	}
}

// runMicrophone negotiates first; cancellation during negotiation prevents capture
// without interrupting signaling for the enclosing stream.
func runMicrophone(streamCtx, micCtx context.Context, platform, source string, s microphoneSession, encode microphoneEncoder, active func()) (error, error) {
	input, err := microphoneInputArgs(platform, source)
	if err != nil {
		return err, nil
	}
	track, err := s.StartMicrophone(streamCtx)
	if err != nil {
		return err, nil
	}
	if err := micCtx.Err(); err != nil {
		return err, s.StopMicrophone()
	}
	announced := false
	err = encode(micCtx, input, func(sample pionmedia.Sample) error {
		if err := micCtx.Err(); err != nil {
			return err
		}
		if err := track.WriteSample(sample); err != nil {
			return err
		}
		if !announced {
			announced = true
			active()
		}
		return nil
	})
	if err == nil && micCtx.Err() == nil {
		err = errors.New("audio capture ended")
	}
	return err, s.StopMicrophone()
}

func microphoneSupported(platform string) bool {
	return platform == "linux" || platform == "darwin" || platform == "windows"
}

func microphoneSourceSettingsSupported(platform string) bool {
	return platform == "linux" || platform == "windows"
}

func microphoneInputArgs(platform, source string) ([]string, error) {
	switch platform {
	case "linux":
		if source == "" {
			source = "default"
		}
		return []string{"-f", "pulse", "-i", source}, nil
	case "darwin":
		// AVFoundation's audio-only default avoids opening a camera.
		return []string{"-f", "avfoundation", "-i", ":default"}, nil
	case "windows":
		if source == "" || source == "default" {
			return nil, errWindowsMicrophoneNotSelected
		}
		if strings.ContainsAny(source, ":\x00\r\n") {
			return nil, errors.New("invalid DirectShow input name")
		}
		return []string{"-f", "dshow", "-i", "audio=" + source}, nil
	default:
		return nil, errors.New("microphone capture is unavailable on this platform")
	}
}

func (a *app) saveMicrophoneSource() {
	source := strings.TrimSpace(a.micSourceDraft)
	if source == "" {
		source = "default"
	}
	next := a.prefs
	next.MicrophoneSource = source
	if err := next.Validate(); err != nil {
		a.status = "Invalid microphone input name."
		return
	}
	if a.settingsPath != "" {
		if err := settings.Save(a.settingsPath, next); err != nil {
			a.status = "Unable to save settings: " + err.Error()
			return
		}
	}
	a.prefs = next
	a.micSourceDraft = source
	_, _, a.micSourceChoice = microphoneSourceOptions(a.micSources, source)
	a.status = "Microphone input saved."
}

// startMicrophone and clearMicrophone run on the UI thread.
func (a *app) startMicrophone() {
	if a.micSession == nil || a.streamCtx == nil || a.micCancel != nil {
		return
	}
	streamCtx, s, source := a.streamCtx, a.micSession, a.prefs.MicrophoneSource
	if source == "" {
		source = "default"
	}
	micCtx, cancel := context.WithCancel(streamCtx)
	a.micCtx, a.micCancel = micCtx, cancel
	a.micStatus = "Starting microphone…"
	if a.streamWG != nil {
		a.streamWG.Add(1)
	}
	go a.captureMicrophone(streamCtx, micCtx, cancel, s, source)
}

func (a *app) captureMicrophone(streamCtx, micCtx context.Context, cancel context.CancelFunc, s *session.Session, source string) {
	if a.streamWG != nil {
		defer a.streamWG.Done()
	}
	defer cancel()
	finished := make(chan struct{})
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-finished:
		}
	}()
	defer close(finished)
	err, stopErr := runMicrophone(streamCtx, micCtx, runtime.GOOS, source, liveMicrophoneSession{s}, media.EncodeMicrophone, func() {
		a.update(func() {
			if a.micCtx == micCtx && micCtx.Err() == nil {
				a.micStatus = "Microphone on."
			}
		})
	})
	wasCanceled := micCtx.Err() != nil
	a.update(func() {
		if a.micCtx != micCtx {
			return
		}
		a.micCtx, a.micCancel = nil, nil
		a.micStopping = false
		a.micStatus = ""
		if stopErr != nil {
			a.micStatus = fmt.Sprintf("Microphone failed: %v", stopErr)
		} else if errors.Is(err, errWindowsMicrophoneNotSelected) && !wasCanceled {
			a.micStatus = errWindowsMicrophoneNotSelected.Error()
		} else if err != nil && !wasCanceled {
			a.micStatus = fmt.Sprintf("Microphone failed: %v", err)
		}
	})
}

func (a *app) clearMicrophone() {
	if a.micCancel != nil {
		a.micCancel()
	}
	a.micSession, a.micCtx, a.micCancel = nil, nil, nil
	a.micStopping = false
	a.micStatus = ""
}
