package main

import (
	"image"
	"sync"
	"testing"
)

func TestFrameMailboxKeepsNewestFrame(t *testing.T) {
	var box frameMailbox
	var queued []func()
	schedule := func(fn func()) { queued = append(queued, fn) }
	var rendered []image.Image
	render := func(frame image.Image) { rendered = append(rendered, frame) }
	first := image.NewRGBA(image.Rect(0, 0, 1, 1))
	latest := image.NewRGBA(image.Rect(0, 0, 2, 2))
	box.offer(first, schedule, render)
	box.offer(latest, schedule, render)
	if len(queued) != 1 {
		t.Fatalf("queued %d updates", len(queued))
	}
	queued[0]()
	if len(rendered) != 1 || rendered[0] != latest {
		t.Fatal("did not render latest frame")
	}
	queued = nil
	box.offer(first, schedule, render)
	if len(queued) != 1 {
		t.Fatal("did not schedule a later frame")
	}
	queued[0]()
	if len(rendered) != 2 || rendered[1] != first {
		t.Fatal("later frame was lost")
	}
}

func TestFrameMailboxConcurrentOffers(t *testing.T) {
	var box frameMailbox
	var mu sync.Mutex
	var queued []func()
	schedule := func(fn func()) { mu.Lock(); queued = append(queued, fn); mu.Unlock() }
	var rendered image.Image
	render := func(frame image.Image) { rendered = frame }
	var workers sync.WaitGroup
	for i := 0; i < 100; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			box.offer(image.NewRGBA(image.Rect(0, 0, 1, 1)), schedule, render)
		}()
	}
	workers.Wait()
	if len(queued) != 1 {
		t.Fatalf("queued %d updates", len(queued))
	}
	queued[0]()
	if rendered == nil {
		t.Fatal("no frame rendered")
	}
}
