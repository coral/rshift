package internal

import (
	"bytes"
	"fmt"
	"net/url"
	"time"

	"github.com/grafov/m3u8"
)

type Hls struct {
	Mp           *m3u8.MediaPlaylist
	playlistUrl  string
	downloadTime int64
	segmentUrls  map[int]string
}

func (h *Hls) fetchPlaylist(playlistUrl string) error {
	var err error
	h.Mp, err = m3u8.NewMediaPlaylist(100000, 100000)
	if err != nil {
		return fmt.Errorf("NewMediaPlaylist: %w", err)
	}

	body, err := download(playlistUrl)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if err = h.Mp.Decode(*bytes.NewBuffer(body), false); err != nil {
		return fmt.Errorf("DecodeFrom: %w", err)
	}
	h.downloadTime = time.Now().Unix()
	h.playlistUrl = playlistUrl

	return nil
}

func (h *Hls) savePlaylist(storage Storage) error {
	return storage.SavePlaylist(h)
}

func (h *Hls) parseSegments() error {
	startAt := 0
	endAt := 2000

	// https://golang.hotexamples.com/examples/github.com.grafov.m3u8/-/NewMediaPlaylist/golang-newmediaplaylist-function-examples.html
	playlistHasMoreItems := func(z int) bool { return (h.Mp.Segments[z] != nil && (z < endAt)) }

	segmentUrls := make(map[int]string, 0)

	segmentCounter := 0
	for i := startAt; playlistHasMoreItems(i); i++ {
		segmentUrl, err := url.Parse(h.Mp.Segments[i].URI)
		if err != nil {
			return fmt.Errorf("Parse segment URL: %w", err)
		}

		if !segmentUrl.IsAbs() {
			base, err := url.Parse(h.playlistUrl)
			if err != nil {
				return fmt.Errorf("Parse base URL: %w", err)
			}
			segmentUrl = base.ResolveReference(segmentUrl)
		}

		segmentUrls[int(h.Mp.SeqNo)+segmentCounter] = segmentUrl.String()
		segmentCounter++
	}

	h.segmentUrls = segmentUrls
	return nil
}

func (h *Hls) fetchAndSaveSegments(storage Storage) error {
	for _, v := range h.segmentUrls {
		b, err := download(v)
		if err != nil {
			return fmt.Errorf("Download: %w", err)
		}

		_ = storage.SaveSegment(h, v, b)
	}
	return nil
}

func (h *Hls) blockTillExpires() {
	secondsToSleep := int(h.Mp.TargetDuration) - 1
	if secondsToSleep == 0 {
		secondsToSleep = 1
	}
	time.Sleep(time.Duration(int64(time.Second) * int64(secondsToSleep)))
}

func (h *Hls) fetchAndSaveAll(storage Storage) error {
	err := h.savePlaylist(storage)
	if err != nil {
		return fmt.Errorf("savePlaylist: %w", err)
	}

	err = h.parseSegments()
	if err != nil {
		return fmt.Errorf("parseSegments: %w", err)
	}

	err = h.fetchAndSaveSegments(storage)
	if err != nil {
		return fmt.Errorf("fetchAndSaveSegments: %w", err)
	}
	return nil
}
