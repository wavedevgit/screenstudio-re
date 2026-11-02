//go:build linux

package main

import "testing"

func TestOfferLatestDropsStaleFrame(t *testing.T) {
	frames := make(chan previewFrame, 1)
	offerLatest(frames, previewFrame{position: 1})
	offerLatest(frames, previewFrame{position: 2})
	if got := (<-frames).position; got != 2 {
		t.Fatalf("got frame %.0f, want latest frame", got)
	}
}
