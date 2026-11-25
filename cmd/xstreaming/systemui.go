package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/peer"
	"github.com/oroliy/xstreamingdesktop-go/protocol"
)

type systemButton struct {
	label string
	index int
}

// The service and original client accept button indices as numbers or strings.
type optionalIndex struct{ value *int }

func (v *optionalIndex) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var n int
	if json.Unmarshal(data, &n) == nil {
		v.value = &n
		return nil
	}
	var text string
	if json.Unmarshal(data, &text) == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			v.value = &n
		}
	}
	return nil
}

type systemRequest interface {
	Done() <-chan struct{}
	TargetName() string
	Payload() string
	Complete(any) error
	Cancel() error
}
type systemPrompt struct {
	request                  systemRequest
	open                     bool
	title, description, text string
	password                 bool
	maxLength                int
	buttons                  []systemButton
	cancelIndex              *int
	streamCtx                context.Context
	restoreKeyboard          bool
}

func parseSystemPrompt(r systemRequest) (*systemPrompt, error) {
	var data struct {
		TitleText       string            `json:"TitleText"`
		ContentText     string            `json:"ContentText"`
		DescriptionText string            `json:"DescriptionText"`
		DefaultText     string            `json:"DefaultText"`
		InputScope      int               `json:"InputScope"`
		MaxLength       int               `json:"MaxLength"`
		CommandLabel1   string            `json:"CommandLabel1"`
		CommandLabel2   string            `json:"CommandLabel2"`
		CommandLabel3   string            `json:"CommandLabel3"`
		CommandLabel4   string            `json:"CommandLabel4"`
		DefaultIndex    optionalIndex     `json:"DefaultIndex"`
		CancelIndex     optionalIndex     `json:"CancelIndex"`
		Commands        []json.RawMessage `json:"Commands"`
	}
	if err := json.Unmarshal([]byte(r.Payload()), &data); err != nil {
		return nil, fmt.Errorf("invalid system UI payload: %w", err)
	}
	p := &systemPrompt{request: r, open: true, title: data.TitleText, cancelIndex: data.CancelIndex.value}
	switch r.TargetName() {
	case peer.SystemUIVirtualKeyboard:
		p.description, p.text, p.password = data.DescriptionText, data.DefaultText, data.InputScope == 31
		if data.MaxLength > 0 {
			p.maxLength = min(data.MaxLength, 4096)
		} else {
			p.maxLength = 4096
		}
	case peer.SystemUIMessageDialog:
		p.description = data.ContentText
		for i, label := range []string{data.CommandLabel1, data.CommandLabel2, data.CommandLabel3, data.CommandLabel4} {
			if label = strings.TrimSpace(label); label != "" {
				p.buttons = append(p.buttons, systemButton{label, i})
			}
		}
		if len(p.buttons) == 0 {
			for i, raw := range data.Commands {
				if i >= 8 {
					break
				}
				var label string
				index := i
				if json.Unmarshal(raw, &label) != nil {
					var command struct {
						Label  string        `json:"Label"`
						Text   string        `json:"Text"`
						Result optionalIndex `json:"Result"`
						Index  optionalIndex `json:"Index"`
					}
					if json.Unmarshal(raw, &command) != nil {
						continue
					}
					label = command.Label
					if label == "" {
						label = command.Text
					}
					if command.Result.value != nil {
						index = *command.Result.value
					} else if command.Index.value != nil {
						index = *command.Index.value
					}
				}
				if label = strings.TrimSpace(label); label != "" {
					p.buttons = append(p.buttons, systemButton{label, index})
				}
			}
		}
		if p.cancelIndex == nil {
			p.cancelIndex = data.DefaultIndex.value
		}
	default:
		return nil, errors.New("unsupported system UI")
	}
	return p, nil
}

func (a *app) showSystemPrompt(c *ui.Context) {
	p := a.systemPrompt
	if p == nil {
		return
	}
	select {
	case <-p.request.Done():
		a.dismissSystemPrompt(p)
		return
	default:
	}
	modal := ui.Modal(c, &p.open, func() {
		_, height := c.Size()
		ui.Scroll(c.Key("system-ui-scroll")).MaxHeight(max(80, height-100)).Gap(8).Children(func() {
			if p.title != "" {
				ui.Text(c, p.title).Bold()
			}
			if p.description != "" {
				ui.Text(c, p.description)
			}
			if p.request.TargetName() == peer.SystemUIVirtualKeyboard {
				input := ui.TextInput(c.Key("system-ui-text"), &p.text).Label(a.tr("Text input")).MaxLength(p.maxLength).Width(320).AutoFocus()
				if p.password {
					input.Password()
				}
				if ui.Button(c.Key("system-ui-submit"), a.tr("Submit")).Clicked() {
					a.finishSystemPrompt(p, map[string]string{"Text": p.text}, false)
				}
			} else {
				for i, button := range p.buttons {
					if ui.Button(c.Key(fmt.Sprintf("system-ui-button-%d", i)), button.label).Clicked() {
						a.finishSystemPrompt(p, map[string]int{"Result": button.index}, false)
					}
				}
			}
			if ui.Button(c.Key("system-ui-cancel"), a.tr("Cancel")).Clicked() {
				a.cancelSystemPrompt(p)
			}
		})
	})
	if modal.Dismissed() && a.systemPrompt == p {
		a.cancelSystemPrompt(p)
	}
}

func (a *app) cancelSystemPrompt(p *systemPrompt) {
	if p.request.TargetName() == peer.SystemUIMessageDialog && p.cancelIndex != nil {
		a.finishSystemPrompt(p, map[string]int{"Result": *p.cancelIndex}, false)
	} else {
		a.finishSystemPrompt(p, nil, true)
	}
}
func (a *app) finishSystemPrompt(p *systemPrompt, value any, cancel bool) {
	if a.systemPrompt != p {
		return
	}
	a.dismissSystemPrompt(p)
	var err error
	if cancel {
		err = p.request.Cancel()
	} else {
		err = p.request.Complete(value)
	}
	if err != nil {
		a.status = "Unable to answer system request: " + err.Error()
	}
}

func (a *app) dismissSystemPrompt(p *systemPrompt) {
	if a.systemPrompt != p {
		for i, queued := range a.systemQueue {
			if queued == p {
				copy(a.systemQueue[i:], a.systemQueue[i+1:])
				a.systemQueue[len(a.systemQueue)-1] = nil
				a.systemQueue = a.systemQueue[:len(a.systemQueue)-1]
				return
			}
		}
		return
	}
	a.systemPrompt = nil
	if p.restoreKeyboard && p.streamCtx != nil && p.streamCtx.Err() == nil && a.streamCtx == p.streamCtx && a.streamCancel != nil {
		a.keyboardEnabled = true
	}
	a.showNextSystemPrompt()
}

func (a *app) enqueueSystemPrompt(p *systemPrompt) {
	a.systemQueue = append(a.systemQueue, p)
	a.showNextSystemPrompt()
}

func (a *app) showNextSystemPrompt() {
	for a.systemPrompt == nil && len(a.systemQueue) > 0 {
		p := a.systemQueue[0]
		a.systemQueue[0] = nil
		a.systemQueue = a.systemQueue[1:]
		select {
		case <-p.request.Done():
			continue
		default:
		}
		if p.streamCtx == nil || p.streamCtx.Err() != nil || a.streamCtx != p.streamCtx || a.streamCancel == nil {
			_ = p.request.Cancel()
			continue
		}
		p.restoreKeyboard = a.keyboardEnabled
		a.keyboardEnabled = false
		a.keyboard.Set(false, protocol.Gamepad{})
		a.keyboard.ClearNexus()
		a.systemPrompt = p
	}
}
