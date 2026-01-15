package internal

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const chunkDurationSeconds = 5

// ADTS sync word is 0xFFF (12 bits)
// Returns the index of the start of an ADTS frame, or -1 if not found
func findADTSSyncWord(data []byte, start int) int {
	for i := start; i < len(data)-1; i++ {
		if data[i] == 0xFF && (data[i+1]&0xF0) == 0xF0 {
			return i
		}
	}
	return -1
}

// Parse ADTS header to get frame length
// Returns frame length (including header) or 0 if invalid
func getADTSFrameLength(data []byte, offset int) int {
	if offset+7 > len(data) {
		return 0
	}
	// Frame length is 13 bits starting at bit 30 (bytes 3-5)
	// Byte 3: bits 6-7 are high 2 bits of length
	// Byte 4: all 8 bits are middle bits
	// Byte 5: bits 5-7 are low 3 bits of length
	length := (int(data[offset+3]&0x03) << 11) |
		(int(data[offset+4]) << 3) |
		(int(data[offset+5]&0xE0) >> 5)
	return length
}

// Find the last complete ADTS frame boundary in data
// Returns the byte offset where we should cut (start of incomplete frame or end of data)
func findLastFrameBoundary(data []byte) int {
	pos := findADTSSyncWord(data, 0)
	if pos == -1 {
		return len(data)
	}

	lastValidEnd := pos
	for pos < len(data) {
		frameLen := getADTSFrameLength(data, pos)
		if frameLen == 0 || pos+frameLen > len(data) {
			// Incomplete frame - cut here
			break
		}
		lastValidEnd = pos + frameLen
		pos = lastValidEnd
	}
	return lastValidEnd
}

func LoopRawStream(url string) error {
	EnsureDirectories()
	os.MkdirAll(filepath.Join(OutPath, "raw"), 0755)

	for {
		err := downloadRawStream(url)
		log.Printf("raw stream error: %v, reconnecting...", err)
		time.Sleep(time.Second)
	}
}

func downloadRawStream(url string) error {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	log.Printf("connected to raw stream, content-type: %s", resp.Header.Get("Content-Type"))

	buf := make([]byte, 32*1024)
	var chunkBuf []byte
	chunkStart := time.Now().Unix()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunkBuf = append(chunkBuf, buf[:n]...)

			if time.Now().Unix()-chunkStart >= chunkDurationSeconds {
				// Find last complete ADTS frame boundary
				cutPoint := findLastFrameBoundary(chunkBuf)
				if cutPoint > 0 {
					if err := saveRawChunk(chunkStart, chunkBuf[:cutPoint]); err != nil {
						log.Printf("save chunk error: %v", err)
					}
					// Keep remainder for next chunk
					chunkBuf = append([]byte(nil), chunkBuf[cutPoint:]...)
					chunkStart = time.Now().Unix()
					cleanOldRawChunks()
				}
			}
		}
		if err == io.EOF {
			return fmt.Errorf("stream ended")
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}
}

func saveRawChunk(timestamp int64, data []byte) error {
	filename := filepath.Join(OutPath, "raw", fmt.Sprintf("%d.aac", timestamp))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return err
	}
	return nil
}

func cleanOldRawChunks() {
	dir := filepath.Join(OutPath, "raw")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Unix() - int64(MaxAgeSeconds)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".aac")
		ts, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			continue
		}
		if ts < cutoff {
			path := filepath.Join(dir, e.Name())
			os.Remove(path)
		}
	}
}

func RawStreamHandler(w http.ResponseWriter, r *http.Request) {
	if TimezoneOffsetSeconds == 0 {
		http.Error(w, "source-timezone not configured", http.StatusServiceUnavailable)
		return
	}

	targetTime := time.Now().Unix() - int64(TimezoneOffsetSeconds)

	dir := filepath.Join(OutPath, "raw")
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "Buffer not ready", http.StatusServiceUnavailable)
		return
	}

	var timestamps []int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".aac")
		ts, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			continue
		}
		timestamps = append(timestamps, ts)
	}

	if len(timestamps) == 0 {
		http.Error(w, "Buffer not ready", http.StatusServiceUnavailable)
		return
	}

	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })

	oldestChunk := timestamps[0]
	startIdx := 0

	if oldestChunk > targetTime {
		// Buffer still growing - start from beginning and stream forward
		log.Printf("buffer still growing (have %d seconds, need %d), streaming from start",
			time.Now().Unix()-oldestChunk, int64(TimezoneOffsetSeconds))
	} else {
		// Full buffer available - find the right starting point
		for i, ts := range timestamps {
			if ts >= targetTime {
				startIdx = i
				break
			}
		}
	}

	w.Header().Set("Content-Type", "audio/aac")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	actualOffset := time.Now().Unix() - timestamps[startIdx]
	log.Printf("streaming from timestamp %d (actual offset %d seconds, target %d)", timestamps[startIdx], actualOffset, TimezoneOffsetSeconds)

	idx := startIdx
	for {
		if idx >= len(timestamps) {
			time.Sleep(time.Second)
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				name := strings.TrimSuffix(e.Name(), ".aac")
				ts, _ := strconv.ParseInt(name, 10, 64)
				if ts > timestamps[len(timestamps)-1] {
					timestamps = append(timestamps, ts)
				}
			}
			sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
			continue
		}

		chunk := filepath.Join(dir, fmt.Sprintf("%d.aac", timestamps[idx]))
		data, err := os.ReadFile(chunk)
		if err != nil {
			idx++
			continue
		}

		_, err = w.Write(data)
		if err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}

		idx++
		time.Sleep(time.Duration(chunkDurationSeconds) * time.Second)
	}
}

const hlsSegmentCount = 10

func RawHLSPlaylistHandler(w http.ResponseWriter, r *http.Request) {
	if TimezoneOffsetSeconds == 0 {
		http.Error(w, "source-timezone not configured", http.StatusServiceUnavailable)
		return
	}

	dir := filepath.Join(OutPath, "raw")
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "Buffer not ready", http.StatusServiceUnavailable)
		return
	}

	var timestamps []int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".aac")
		ts, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			continue
		}
		timestamps = append(timestamps, ts)
	}

	if len(timestamps) == 0 {
		http.Error(w, "Buffer not ready", http.StatusServiceUnavailable)
		return
	}

	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })

	targetTime := time.Now().Unix() - int64(TimezoneOffsetSeconds)
	oldestChunk := timestamps[0]

	startIdx := 0
	if oldestChunk > targetTime {
		// Buffer still growing - start from beginning
		startIdx = 0
	} else {
		for i, ts := range timestamps {
			if ts >= targetTime {
				startIdx = i
				break
			}
		}
	}

	// Get up to hlsSegmentCount segments starting from startIdx
	endIdx := startIdx + hlsSegmentCount
	if endIdx > len(timestamps) {
		endIdx = len(timestamps)
	}
	segments := timestamps[startIdx:endIdx]

	if len(segments) == 0 {
		http.Error(w, "No segments available", http.StatusServiceUnavailable)
		return
	}

	// Build m3u8 playlist
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:3\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", chunkDurationSeconds))
	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", segments[0]))
	playlist.WriteString("\n")

	for _, ts := range segments {
		playlist.WriteString(fmt.Sprintf("#EXTINF:%d.0,\n", chunkDurationSeconds))
		playlist.WriteString(fmt.Sprintf("/raw/segments/%d.aac\n", ts))
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(playlist.String()))
}

func RawHLSSegmentHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if !strings.HasSuffix(filename, ".aac") {
		http.Error(w, "Invalid segment", http.StatusBadRequest)
		return
	}

	segmentPath := filepath.Join(OutPath, "raw", filename)
	data, err := os.ReadFile(segmentPath)
	if err != nil {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "audio/aac")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Write(data)
}
