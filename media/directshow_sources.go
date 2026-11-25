package media

import (
	"context"
	"errors"
	"strings"
)

// DirectShowSources lists devices explicitly reported as audio inputs.
// Discovery does not start audio capture.
func DirectShowSources(ctx context.Context) ([]AudioSource, error) {
	output, err := captureSourceOutput(ctx, "dshow")
	if err != nil {
		return nil, err
	}
	return parseDirectShowSources(output)
}

func parseDirectShowSources(output string) ([]AudioSource, error) {
	if !strings.Contains(output, "Auto-detected sources for dshow:") || strings.Contains(output, "Cannot list sources:") {
		return nil, errors.New("DirectShow inputs unavailable")
	}
	seen := map[string]bool{}
	var sources []AudioSource
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		name, details, ok := strings.Cut(line, " [")
		if !ok || name == "" || len(name) > 256 || strings.ContainsAny(name, ":\x00\r\n") || seen[name] {
			continue
		}
		end := strings.LastIndex(details, "] (")
		if end < 0 || !strings.HasSuffix(details, ")") {
			continue
		}
		kinds := strings.Split(strings.TrimSuffix(details[end+3:], ")"), ",")
		audio := false
		for _, kind := range kinds {
			if strings.TrimSpace(kind) == "audio" {
				audio = true
			}
		}
		if !audio {
			continue
		}
		description := []rune(strings.TrimSpace(details[:end]))
		if len(description) > 160 {
			description = description[:160]
		}
		seen[name] = true
		sources = append(sources, AudioSource{Name: name, Description: string(description)})
	}
	return sources, nil
}
