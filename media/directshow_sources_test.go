package media

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDirectShowSources(t *testing.T) {
	data := "Auto-detected sources for dshow:\n" +
		"  @device_cm_{video}\\camera [Camera] (video)\n" +
		"  @device_cm_{audio}\\wave [USB Microphone] (audio)\n" +
		"  @device_cm_{mixed}\\capture [Capture card] (video, audio)\n" +
		"  @device_cm_{unknown}\\unknown [Unknown] (none)\n" +
		"  @device_cm_{audio}\\wave [Duplicate] (audio)\n" +
		"  bad:source [Invalid] (audio)\n"
	want := []AudioSource{{Name: "@device_cm_{audio}\\wave", Description: "USB Microphone"}, {Name: "@device_cm_{mixed}\\capture", Description: "Capture card"}}
	got, err := parseDirectShowSources(data)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v error=%v", got, err)
	}
	for _, invalid := range []string{"", "Auto-detected sources for dshow:\nCannot list sources: failed"} {
		if _, err := parseDirectShowSources(invalid); err == nil {
			t.Fatalf("accepted error output %q", invalid)
		}
	}
}

func TestDirectShowDescriptionIsBounded(t *testing.T) {
	data := "Auto-detected sources for dshow:\n input [" + strings.Repeat("x", 500) + "] (audio)\n"
	got, err := parseDirectShowSources(data)
	if err != nil || len(got) != 1 || len([]rune(got[0].Description)) != 160 {
		t.Fatalf("got=%v error=%v", got, err)
	}
}
