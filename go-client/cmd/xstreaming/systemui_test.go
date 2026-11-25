package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/go-client/peer"
)

func TestSystemPromptRestoresKeyboardOnlyForActiveStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, tc := range []struct {
		name                  string
		restore, active, same bool
		want                  bool
	}{
		{"previously enabled", true, true, true, true},
		{"previously disabled", false, true, true, false},
		{"stream stopped", true, false, true, false},
		{"another stream", true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{streamCtx: ctx}
			if tc.active {
				a.streamCancel = cancel
			}
			if !tc.same {
				a.streamCtx = context.Background()
			}
			p := &systemPrompt{streamCtx: ctx, restoreKeyboard: tc.restore}
			a.systemPrompt = p
			a.dismissSystemPrompt(p)
			if a.keyboardEnabled != tc.want || a.systemPrompt != nil {
				t.Fatalf("enabled=%v prompt=%v", a.keyboardEnabled, a.systemPrompt)
			}
		})
	}
}

type fakeSystemRequest struct {
	target, payload string
	done            chan struct{}
	result          any
	canceled        bool
}

func (r *fakeSystemRequest) Done() <-chan struct{}    { return r.done }
func (r *fakeSystemRequest) TargetName() string       { return r.target }
func (r *fakeSystemRequest) Payload() string          { return r.payload }
func (r *fakeSystemRequest) Complete(value any) error { r.result = value; close(r.done); return nil }
func (r *fakeSystemRequest) Cancel() error            { r.canceled = true; close(r.done); return nil }

func TestSystemKeyboardPrompt(t *testing.T) {
	r := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: `{"TitleText":"Sign in","DescriptionText":"Enter password","DefaultText":"secret","InputScope":31,"MaxLength":20}`, done: make(chan struct{})}
	prompt, err := parseSystemPrompt(r)
	if err != nil || !prompt.password || prompt.maxLength != 20 {
		t.Fatalf("prompt=%+v error=%v", prompt, err)
	}
	a := &app{systemPrompt: prompt}
	tt := ui.NewTester(a.view, 1024, 768)
	if !tt.HasText("Sign in") || !tt.HasText("Enter password") {
		t.Fatal(tt.Texts())
	}
	if tt.HasText("secret") {
		t.Fatal("password is visible as plain text")
	}
	if err := tt.Click("Submit"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.result, map[string]string{"Text": "secret"}) || a.systemPrompt != nil {
		t.Fatalf("result=%v prompt=%v", r.result, a.systemPrompt)
	}
}

func TestSystemMessagePromptAndCancel(t *testing.T) {
	r := &fakeSystemRequest{target: peer.SystemUIMessageDialog, payload: `{"TitleText":"Choose","ContentText":"Continue?","CommandLabel1":"Yes","CommandLabel2":"No","CancelIndex":1}`, done: make(chan struct{})}
	prompt, err := parseSystemPrompt(r)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{systemPrompt: prompt}
	tt := ui.NewTester(a.view, 1024, 768)
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.result, map[string]int{"Result": 1}) || r.canceled {
		t.Fatalf("result=%v canceled=%v", r.result, r.canceled)
	}
}

func TestInvalidSystemPrompt(t *testing.T) {
	r := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: "bad", done: make(chan struct{})}
	if _, err := parseSystemPrompt(r); err == nil {
		t.Fatal("accepted malformed payload")
	}
}

func TestRemoteCancelRemovesPrompt(t *testing.T) {
	r := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: `{"TitleText":"Remote request"}`, done: make(chan struct{})}
	prompt, err := parseSystemPrompt(r)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{systemPrompt: prompt}
	tt := ui.NewTester(a.view, 1024, 768)
	if !tt.HasText("Remote request") {
		t.Fatal("prompt missing")
	}
	close(r.done)
	tt.Frame()
	if a.systemPrompt != nil || tt.HasText("Remote request") {
		t.Fatal("canceled prompt still visible")
	}
}

func TestSystemMessageFallbackCommands(t *testing.T) {
	r := &fakeSystemRequest{target: peer.SystemUIMessageDialog, payload: `{"Commands":["First",{"label":"Second","result":"7"}],"CancelIndex":"7"}`, done: make(chan struct{})}
	p, err := parseSystemPrompt(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.buttons, []systemButton{{"First", 0}, {"Second", 7}}) || p.cancelIndex == nil || *p.cancelIndex != 7 {
		t.Fatalf("prompt=%+v", p)
	}
}

func TestSystemPromptEscapeRestoresKeyboard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: `{}`, done: make(chan struct{})}
	p, err := parseSystemPrompt(r)
	if err != nil {
		t.Fatal(err)
	}
	p.streamCtx, p.restoreKeyboard = ctx, true
	a := &app{systemPrompt: p, streamCtx: ctx, streamCancel: cancel}
	tt := ui.NewTester(a.view, 1024, 768)
	tt.KeyDown(ui.KeyEscape)
	tt.KeyUp(ui.KeyEscape)
	if !r.canceled || !a.keyboardEnabled || a.systemPrompt != nil {
		t.Fatalf("canceled=%v enabled=%v prompt=%v", r.canceled, a.keyboardEnabled, a.systemPrompt)
	}
}

func TestSystemPromptsAreQueuedAndRestoreKeyboard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{streamCtx: ctx, streamCancel: cancel, keyboardEnabled: true}
	first := &fakeSystemRequest{target: peer.SystemUIMessageDialog, payload: `{"TitleText":"First request","CommandLabel1":"Continue"}`, done: make(chan struct{})}
	second := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: `{"TitleText":"Second request","DefaultText":"answer"}`, done: make(chan struct{})}
	p1, err := parseSystemPrompt(first)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := parseSystemPrompt(second)
	if err != nil {
		t.Fatal(err)
	}
	p1.streamCtx, p2.streamCtx = ctx, ctx
	a.keyboard.PressNexus(time.Minute)
	a.enqueueSystemPrompt(p1)
	if active, _ := a.keyboard.Snapshot(); active {
		t.Fatal("system prompt left the Xbox button pressed")
	}
	a.enqueueSystemPrompt(p2)
	if a.systemPrompt != p1 || len(a.systemQueue) != 1 || a.keyboardEnabled {
		t.Fatal("prompts not queued")
	}
	tt := ui.NewTester(a.view, 1024, 768)
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.systemPrompt != p2 || !tt.HasText("Second request") || a.keyboardEnabled {
		t.Fatal("second prompt not shown")
	}
	if err := tt.Click("Submit"); err != nil {
		t.Fatal(err)
	}
	if !a.keyboardEnabled || a.systemPrompt != nil || len(a.systemQueue) != 0 {
		t.Fatal("keyboard not restored after queue")
	}
}

func TestCanceledQueuedPromptIsSkipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{streamCtx: ctx, streamCancel: cancel, keyboardEnabled: true}
	r1 := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: `{}`, done: make(chan struct{})}
	r2 := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: `{}`, done: make(chan struct{})}
	p1, _ := parseSystemPrompt(r1)
	p2, _ := parseSystemPrompt(r2)
	p1.streamCtx, p2.streamCtx = ctx, ctx
	a.enqueueSystemPrompt(p1)
	a.enqueueSystemPrompt(p2)
	close(r2.done)
	a.dismissSystemPrompt(p2)
	a.finishSystemPrompt(p1, nil, true)
	if a.systemPrompt != nil || len(a.systemQueue) != 0 || !a.keyboardEnabled {
		t.Fatal("canceled queued prompt was shown or keyboard not restored")
	}
}

func TestRemovingQueuedPromptClearsBackingReference(t *testing.T) {
	p := &systemPrompt{text: "sensitive default"}
	a := &app{systemQueue: []*systemPrompt{p}}
	backing := a.systemQueue
	a.dismissSystemPrompt(p)
	if len(a.systemQueue) != 0 || backing[0] != nil {
		t.Fatal("removed prompt is still retained by the queue")
	}
}

func TestSystemKeyboardPasswordRenderingMasksText(t *testing.T) {
	imageFor := func(text string, password bool) []byte {
		scope := 0
		if password {
			scope = 31
		}
		payload, err := json.Marshal(map[string]any{"TitleText": "Entry", "DefaultText": text, "InputScope": scope})
		if err != nil {
			t.Fatal(err)
		}
		r := &fakeSystemRequest{target: peer.SystemUIVirtualKeyboard, payload: string(payload), done: make(chan struct{})}
		p, err := parseSystemPrompt(r)
		if err != nil {
			t.Fatal(err)
		}
		a := &app{systemPrompt: p}
		tt := ui.NewTester(a.view, 1024, 768)
		tt.SetFocused(false)
		return slices.Clone(tt.Image().Pix)
	}
	maskedA, maskedB := imageFor("secret", true), imageFor("xxxxxx", true)
	if !bytes.Equal(maskedA, maskedB) {
		t.Fatal("password rendering depends on the actual characters")
	}
	plainA, plainB := imageFor("secret", false), imageFor("xxxxxx", false)
	if bytes.Equal(plainA, plainB) {
		t.Fatal("comparison cannot distinguish unmasked text")
	}
}
