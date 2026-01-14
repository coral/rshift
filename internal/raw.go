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
				if err := saveRawChunk(chunkStart, chunkBuf); err != nil {
					log.Printf("save chunk error: %v", err)
				}
				chunkBuf = nil
				chunkStart = time.Now().Unix()
				cleanOldRawChunks()
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
	log.Printf("saved chunk %s (%d bytes)", filename, len(data))
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
			log.Printf("deleted old chunk %s", path)
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
	if oldestChunk > targetTime {
		needed := oldestChunk - targetTime
		http.Error(w, fmt.Sprintf("Buffer not ready - need %d more seconds of data", needed), http.StatusServiceUnavailable)
		return
	}

	startIdx := 0
	for i, ts := range timestamps {
		if ts >= targetTime {
			startIdx = i
			break
		}
	}

	w.Header().Set("Content-Type", "audio/aac")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	log.Printf("streaming from timestamp %d (offset %d seconds)", timestamps[startIdx], TimezoneOffsetSeconds)

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
