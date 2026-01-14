package internal

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
)

func EnsureDirectories() {
	os.MkdirAll(filepath.Join(OutPath, "m3u8"), 0755)
	os.MkdirAll(filepath.Join(OutPath, "ts"), 0755)
}

func downloadPlaylistIfRecent(playlistUrl string, lastSequence int) (int, error) {
	BackendFs = afero.NewOsFs()
	fs := &FileStorage{}

	var h Hls
	err := h.fetchPlaylist(playlistUrl)
	if err != nil {
		return 0, fmt.Errorf("fetchPlaylist: %w", err)
	}

	if int(h.Mp.SeqNo) != lastSequence {
		err = h.fetchAndSaveAll(fs)
		if err != nil {
			return 0, fmt.Errorf("fetchAndSaveAll: %w", err)
		}
		h.blockTillExpires()
		return int(h.Mp.SeqNo), nil
	}

	return lastSequence, nil
}

func LoopPlayList(url string) error {
	EnsureDirectories()
	lastSequence := 0
	for {
		var err error
		lastSequence, err = downloadPlaylistIfRecent(url, lastSequence)
		if err != nil {
			fmt.Println(err)
			return err
		}
	}
}
