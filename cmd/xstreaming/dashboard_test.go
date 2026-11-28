package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/settings"
	"github.com/oroliy/xstreamingdesktop-go/streaming"
	"testing"
)

func TestDashboardNavigation(t *testing.T) {
	a := &app{prefs: settings.Defaults(), titles: []streaming.Title{{TitleID: "1", Name: "Halo"}}}
	tt := ui.NewTester(a.view, 800, 700)
	if !tt.HasText("Your consoles") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Cloud games"); err != nil {
		t.Fatal(err)
	}
	if a.page != "cloud" || !tt.HasText("Halo") {
		t.Fatal(a.page, tt.Texts())
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if a.page != "settings" || !tt.HasText("Resolution") {
		t.Fatal(a.page, tt.Texts())
	}
	if err := tt.Click("Consoles"); err != nil {
		t.Fatal(err)
	}
	if a.page != "" || !tt.HasText("Your consoles") {
		t.Fatal(a.page, tt.Texts())
	}
}
