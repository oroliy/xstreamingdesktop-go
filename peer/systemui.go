package peer

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/oroliy/xstreamingdesktop-go/protocol"
)

const (
	SystemUIMessageDialog   = "/streaming/systemUi/messages/ShowMessageDialog"
	SystemUIVirtualKeyboard = "/streaming/systemUi/messages/ShowVirtualKeyboard"
)

// SystemUIRequest represents one service-requested dialog. Done closes when the
// request is answered, canceled remotely, or the session ends.
type SystemUIRequest struct {
	ID, Target, Content string
	done                chan struct{}
	finish              func(any, bool) error
}

func (r *SystemUIRequest) Done() <-chan struct{}    { return r.done }
func (r *SystemUIRequest) TargetName() string       { return r.Target }
func (r *SystemUIRequest) Payload() string          { return r.Content }
func (r *SystemUIRequest) Complete(value any) error { return r.finish(value, false) }
func (r *SystemUIRequest) Cancel() error            { return r.finish(nil, true) }

type systemUIRequests struct {
	mu      sync.Mutex
	ctx     context.Context
	pending map[string]*SystemUIRequest
	closed  bool
	send    func(protocol.Envelope) error
	onUI    func(*SystemUIRequest)
}

func newSystemUIRequests(ctx context.Context, send func(protocol.Envelope) error, onUI func(*SystemUIRequest)) *systemUIRequests {
	s := &systemUIRequests{ctx: ctx, pending: make(map[string]*SystemUIRequest), send: send, onUI: onUI}
	context.AfterFunc(ctx, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		for id, r := range s.pending {
			delete(s.pending, id)
			close(r.done)
		}
	})
	return s
}

func (s *systemUIRequests) handle(in protocol.Envelope) bool {
	if in.Type == "SenderCancel" {
		s.mu.Lock()
		defer s.mu.Unlock()
		r := s.pending[in.ID]
		if r == nil {
			return false
		}
		delete(s.pending, in.ID)
		close(r.done)
		return true
	}
	if s.onUI == nil || (in.Type != "Message" && in.Type != "TransactionStart") || in.ID == "" || (in.Target != SystemUIMessageDialog && in.Target != SystemUIVirtualKeyboard) {
		return false
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil || len(s.pending) >= 8 || s.pending[in.ID] != nil {
		s.mu.Unlock()
		return false
	}
	r := &SystemUIRequest{ID: in.ID, Target: in.Target, Content: in.Content, done: make(chan struct{})}
	r.finish = func(value any, canceled bool) error {
		var content string
		if !canceled && in.Type == "TransactionStart" {
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			content = string(data)
		}
		s.mu.Lock()
		if s.closed || s.ctx.Err() != nil || s.pending[in.ID] != r {
			s.mu.Unlock()
			return errors.New("system UI request is no longer active")
		}
		delete(s.pending, in.ID)
		close(r.done)
		s.mu.Unlock()
		if in.Type != "TransactionStart" {
			return nil
		}
		typ := "TransactionComplete"
		if canceled {
			typ = "ReceiverCancel"
		}
		cv := advanceSystemUICV(advanceSystemUICV(in.CV))
		return s.send(protocol.Envelope{Type: typ, ID: in.ID, CV: cv, Content: content})
	}
	s.pending[in.ID] = r
	s.mu.Unlock()
	s.onUI(r)
	return true
}

func advanceSystemUICV(cv string) string {
	if cv == "" {
		return strings.ReplaceAll(uuid.NewString(), "-", "") + ".1"
	}
	parts := strings.Split(cv, ".")
	n, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	if err != nil || n == math.MaxUint64 {
		return cv + ".1"
	}
	parts[len(parts)-1] = strconv.FormatUint(n+1, 10)
	return strings.Join(parts, ".")
}
