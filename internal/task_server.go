package internal

import (
	"compress/gzip"
	"crypto/subtle"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

var storage Storage

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func TimeshiftHandler(w http.ResponseWriter, r *http.Request) {
	timeShift := r.PathValue("timeShift")
	timeShift = strings.TrimSuffix(timeShift, ".m3u8")

	ts, err := strconv.Atoi(timeShift)
	if err != nil {
		log.Printf("Atoi returned error converting %s\n", timeShift)
		return
	}
	startTime := time.Now()

	fileBytes, err := storage.ReadPlaylistNear(int(time.Now().Unix()) - ts)
	endTime := time.Now()

	log.Println("ReadPlaylistNear took: ", endTime.Sub(startTime))

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.WriteHeader(http.StatusOK)
	w.Write(fileBytes)
}

func DefaultTimeshiftHandler(w http.ResponseWriter, r *http.Request) {
	if TimezoneOffsetSeconds == 0 {
		http.Error(w, "source-timezone not configured", http.StatusServiceUnavailable)
		return
	}

	targetTime := int(time.Now().Unix()) - TimezoneOffsetSeconds
	fileBytes, err := storage.ReadPlaylistNear(targetTime)
	if err != nil || len(fileBytes) == 0 {
		http.Error(w, "Buffer not ready", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Write(fileBytes)
}

func gzipHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		h.ServeHTTP(gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

func corsHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func basicAuthHandler(h http.Handler, username, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func MainServer() {
	storage = &FileStorage{}

	u, err := url.Parse(M3u8DownloadUrl)
	if err != nil {
		panic(err)
	}
	urlDirectory := path.Dir(u.Path)

	mux := http.NewServeMux()
	if RawMode {
		mux.HandleFunc("GET /stream", RawStreamHandler)
	} else {
		mux.HandleFunc("GET /shift/{timeShift...}", TimeshiftHandler)
		mux.HandleFunc("GET /default.m3u8", DefaultTimeshiftHandler)
		mux.Handle("GET /segments/", http.StripPrefix("/segments/",
			http.FileServer(http.Dir(path.Join(OutPath, path.Join("ts/", urlDirectory))))))
	}

	handler := gzipHandler(corsHandler(mux))
	if os.Getenv("RSHIFT_USERNAME") != "" {
		handler = basicAuthHandler(handler, os.Getenv("RSHIFT_USERNAME"), os.Getenv("RSHIFT_PASSWORD"))
	} else {
		log.Println("warning: proceeding without any authentication")
	}
	http.ListenAndServe(":8080", handler)
}
