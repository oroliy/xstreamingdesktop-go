package main

import (
	"image"
	"sync"
)

// frameMailbox keeps only the newest decoded frame while a UI update is pending.
type frameMailbox struct {
	mu        sync.Mutex
	latest    image.Image
	scheduled bool
}

func (m *frameMailbox) offer(frame image.Image, schedule func(func()), render func(image.Image)) {
	m.mu.Lock()
	m.latest = frame
	if m.scheduled {
		m.mu.Unlock()
		return
	}
	m.scheduled = true
	m.mu.Unlock()
	schedule(func() {
		m.mu.Lock()
		frame := m.latest
		m.latest = nil
		m.scheduled = false
		m.mu.Unlock()
		if frame != nil {
			render(frame)
		}
	})
}
