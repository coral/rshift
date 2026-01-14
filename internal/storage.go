package internal

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/afero"
)

type Storage interface {
	SavePlaylist(h *Hls) error
	SaveSegment(h *Hls, url string, segment []byte) error
	ReadPlaylistNear(desiredTimestamp int) ([]byte, error)
}

type FileStorage struct {}

var BackendFs afero.Fs

func (d *FileStorage) SavePlaylist(h *Hls) error {
	fileName, err := getPlaylistFilename(h.playlistUrl, h.downloadTime)
	if err != nil {
		return fmt.Errorf("getPlaylistFilename: %w", err)
	}

	_ = BackendFs.MkdirAll(filepath.Dir(fileName), os.ModeDir|os.ModePerm)
	out, err := BackendFs.Create(fileName)
	if err != nil {
		return fmt.Errorf("Create: %w", err)
	}
	defer out.Close()

	_, err = h.Mp.Encode().WriteTo(out)
	if err != nil {
		return fmt.Errorf("WriteTo: %w", err)
	}
	return nil
}

func (d *FileStorage) SaveSegment(h *Hls, segmentUrl string, segment []byte) error {
	fileName, err := getSegmentFilename(segmentUrl)
	if err != nil {
		return fmt.Errorf("getSegmentFilename: %w", err)
	}
	if _, err := BackendFs.Stat(fileName); err == nil {
		return nil
	}
	_ = BackendFs.MkdirAll(filepath.Dir(fileName), os.ModeDir|os.ModePerm)

	out, err := BackendFs.Create(fileName)
	if err != nil {
		return fmt.Errorf("Create: %w", err)
	}
	defer out.Close()

	_, err = out.Write(segment)
	if err != nil {
		return fmt.Errorf("Write: %w", err)
	}
	return nil
}

func (d *FileStorage) ReadPlaylistNear(desiredTimestamp int) ([]byte, error) {
	fileName, err := GetClosestPlaylistFilename(desiredTimestamp)
	if err != nil {
		return nil, fmt.Errorf("GetClosestPlaylistFilename: %w", err)
	}

	fileBytes, err := afero.ReadFile(BackendFs, fileName)
	if err != nil {
		return nil, fmt.Errorf("ReadFile: %w", err)
	}

	return fileBytes, nil
}

const SEPARATOR = "---"

func getPlaylistFilename(playListUrl string, unixTime int64) (string, error) {
	u, err := url.Parse(playListUrl)
	if err != nil {
		return "", fmt.Errorf("Parse: %w", err)
	}
	fileName := filepath.Join(OutPath, "m3u8", path.Base(u.Path)+SEPARATOR+strconv.FormatInt(unixTime, 10))
	return fileName, nil
}

func GetClosestPlaylistFilename(approxTime int) (string, error) {
	dir, err := BackendFs.Open(filepath.Join(OutPath, "m3u8"))
	if err != nil {
		return "", fmt.Errorf("Open: %w", err)
	}
	defer dir.Close()
	fileNames, err := dir.Readdirnames(0)
	if err != nil {
		return "", fmt.Errorf("Readdir: %w", err)
	}
	sort.Strings(fileNames)

	for _, fn := range fileNames {
		fields := strings.Split(fn, SEPARATOR)
		whenDownloaded, _ := strconv.Atoi(fields[1])
		if whenDownloaded > approxTime {
			difference := whenDownloaded-approxTime
			log.Printf("getClosestPlaylist returns %s (wanted: %d got: %d difference: %d)\n",
				fn,
				approxTime,
				whenDownloaded,
				difference)

			if difference > 60 {
				// really should be an error, but do not want to tear everything down
				log.Printf("difference %v is too large\n", difference)
			}

			return filepath.Join(filepath.Join(OutPath, "m3u8"), fn), nil
		}
	}
	return "", nil
}

func getSegmentFilename(segmentUrl string) (string, error) {
	u, err := url.Parse(segmentUrl)
	if err != nil {
		return "", fmt.Errorf("Parse: %w", err)
	}
	fileName := filepath.Join(OutPath, "ts", u.Path)
	return fileName, nil
}
