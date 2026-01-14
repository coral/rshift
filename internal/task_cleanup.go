package internal

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

func DeleteOldFiles() {
	wipeDir(filepath.Join(OutPath, "m3u8"))
	wipeDir(filepath.Join(OutPath, "ts"))
}

func wipeDir(dir string) {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.ModTime().Add(time.Duration(MaxAgeSeconds) * time.Second).Before(time.Now()) {
			log.Printf("deleting file %s (too old)\n", path)
			if err := os.Remove(path); err != nil {
				log.Printf("error: Remove %s %v\n", path, err)
			}
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}
}
