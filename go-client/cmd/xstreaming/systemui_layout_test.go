package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/go-client/peer"
)

func TestLongSystemDialogCanScrollToCancel(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"TitleText": "Title", "ContentText": strings.Repeat("Long description ", 30),
		"CommandLabel1": "First", "CommandLabel2": "Second", "CommandLabel3": "Third", "CommandLabel4": "Fourth",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeSystemRequest{target: peer.SystemUIMessageDialog, payload: string(data), done: make(chan struct{})}
	p, err := parseSystemPrompt(r)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{systemPrompt: p}
	tt := ui.NewTester(a.view, 640, 400)
	tt.Scroll(320, 200, 0, 1000)
	rect, ok := tt.Find("Cancel")
	if !ok || rect.W <= 0 || rect.H <= 0 || rect.Y < 0 || rect.Y+rect.H > 400 {
		t.Fatalf("cancel button is not visible: %+v, found=%v", rect, ok)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if !r.canceled {
		t.Fatal("cancel was not sent")
	}
}
