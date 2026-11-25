// Package protocol implements Xbox streaming data-channel messages.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type Envelope struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	ID      string `json:"id"`
	CV      string `json:"cv"`
	Target  string `json:"target,omitempty"`
	Content string `json:"content,omitempty"`
}
type MessageChannel struct {
	mu                sync.Mutex
	prefix, installID string
	counter           int
	ready             bool
}

func NewMessageChannel() *MessageChannel {
	return &MessageChannel{prefix: strings.ReplaceAll(uuid.NewString(), "-", ""), installID: uuid.NewString()}
}
func (m *MessageChannel) next() string { m.counter++; return fmt.Sprintf("%s.%d", m.prefix, m.counter) }
func (m *MessageChannel) Handshake() Envelope {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Envelope{Type: "Handshake", Version: "messageV1", ID: uuid.NewString(), CV: m.next()}
}
func (m *MessageChannel) message(target string, data any) Envelope {
	b, _ := json.Marshal(data)
	return Envelope{Type: "Message", ID: uuid.NewString(), CV: m.next(), Target: target, Content: string(b)}
}

// HandleHandshake validates the server response and generates the initial client configuration.
func (m *MessageChannel) HandleHandshake(data []byte) ([]Envelope, error) {
	var ack Envelope
	if err := json.Unmarshal(data, &ack); err != nil {
		return nil, err
	}
	if ack.Type != "HandshakeAck" || ack.Version != "messageV1" {
		return nil, errors.New("invalid message channel handshake acknowledgement")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready {
		return nil, nil
	}
	m.ready = true
	return []Envelope{
		m.message("/streaming/systemUi/configuration", map[string]any{"version": []int{0, 2, 0}, "systemUis": []int{10, 19, 31, 27, 32, -44, 40, 41, -43}}),
		m.message("/streaming/properties/clientappinstallidchanged", map[string]string{"clientAppInstallId": m.installID}),
		m.message("/streaming/characteristics/orientationchanged", map[string]int{"orientation": 0}),
		m.message("/streaming/characteristics/touchinputenabledchanged", map[string]bool{"touchInputEnabled": false}),
		m.message("/streaming/characteristics/clientdevicecapabilities", map[string]any{}),
		m.message("/streaming/characteristics/dimensionschanged", map[string]any{"horizontal": 1920, "vertical": 1080, "preferredWidth": 1920, "preferredHeight": 1080, "safeAreaLeft": 0, "safeAreaTop": 0, "safeAreaRight": 1920, "safeAreaBottom": 1080, "supportsCustomResolution": true}),
	}, nil
}
func Authorization() map[string]any {
	return map[string]any{"message": "authorizationRequest", "accessKey": "4BDB3609-C1F1-4195-9B37-FEFF45DA8B8E"}
}
func GamepadChanged(index int, added bool) map[string]any {
	return map[string]any{"message": "gamepadChanged", "gamepadIndex": index, "wasAdded": added}
}
func KeyframeRequest() map[string]any {
	return map[string]any{"message": "videoKeyframeRequested", "ifrRequested": true}
}

// Incoming returns the protocol response for an unsupported service message.
// Touch-control transactions are canceled because this client has no touch input.
func (m *MessageChannel) Incoming(in Envelope) *Envelope {
	if in.Type != "Message" && in.Type != "TransactionStart" {
		return nil
	}
	if in.ID == "" || in.Target == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := func(cv string) string {
		if cv == "" {
			return m.next()
		}
		parts := strings.Split(cv, ".")
		last, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
		if err != nil || last == math.MaxUint64 {
			return cv + ".1"
		}
		parts[len(parts)-1] = strconv.FormatUint(last+1, 10)
		return strings.Join(parts, ".")
	}
	if strings.HasPrefix(in.Target, "/streaming/touchcontrols") {
		if in.Type == "Message" {
			return nil
		}
		return &Envelope{Type: "ReceiverCancel", ID: in.ID, CV: next(next(in.CV))}
	}
	return &Envelope{Type: "Unhandled", ID: in.ID, Target: in.Target, CV: next(in.CV)}
}
