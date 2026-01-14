package main

import (
	"flag"
	"log"
	"rshift/internal"
	"sync"
	"time"
)

func calculateTimezoneOffset(sourceTZ string) (int, error) {
	loc, err := time.LoadLocation(sourceTZ)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	_, sourceOffset := now.In(loc).Zone()
	_, localOffset := now.Zone()
	return sourceOffset - localOffset, nil
}

func workerDownload(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		err := internal.LoopPlayList(internal.M3u8DownloadUrl)
		log.Printf("download error: %v, restarting...\n", err)
	}
}

func workerServer(wg *sync.WaitGroup) {
	defer wg.Done()
	internal.MainServer()
}

func workerCleanup(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		internal.DeleteOldFiles()
		time.Sleep(time.Hour)
	}
}



func main() {
	flag.StringVar(&internal.M3u8DownloadUrl, "download-m3u8-url", "https://example.com/file.m3u8", "URL with M3U8 playlist")
	flag.StringVar(&internal.OutPath, "output-path", "/mnt/disks/sdb/out", "path to store cached files")
	flag.StringVar(&internal.SourceTimezone, "source-timezone", "", "IANA timezone of source stream (e.g., Europe/Stockholm)")
	flag.BoolVar(&internal.MinimalBuffer, "minimal-buffer", false, "only keep enough buffer for timezone delta + 2 min")
	flag.Parse()

	if internal.SourceTimezone != "" {
		offset, err := calculateTimezoneOffset(internal.SourceTimezone)
		if err != nil {
			log.Fatalf("invalid source-timezone %q: %v", internal.SourceTimezone, err)
		}
		internal.TimezoneOffsetSeconds = offset
		log.Printf("timezone offset: %d seconds (%s vs local)", offset, internal.SourceTimezone)

		if internal.MinimalBuffer {
			internal.MaxAgeSeconds = offset + 120
			log.Printf("minimal buffer mode: keeping %d seconds of data", internal.MaxAgeSeconds)
		}
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go workerDownload(&wg)
	go workerServer(&wg)
	go workerCleanup(&wg)
	wg.Wait()
	log.Println("Main: Completed")
}
