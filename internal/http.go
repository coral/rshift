package internal

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

func download(url string) ([]byte, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("NewRequest: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 12_2_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.2 Safari/605.1.15")
	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error downloading %s: HTTP status %v != %v", url, resp.StatusCode, http.StatusOK)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ReadAll: %w", err)
	}
	return bodyBytes, nil
}
