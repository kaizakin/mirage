package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxAttempts = 3

// postJSON sends payload to endpoint and returns the body of a 200 reply. Providers answer
// 429 and 503 when busy or briefly over a rate limit; those clear in seconds, so they are
// retried with a short backoff rather than failing the user's whole chat turn. name labels
// errors with the provider.
func postJSON(ctx context.Context, client *http.Client, name, endpoint string, header http.Header, payload []byte) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		raw, retryable, err := postOnce(ctx, client, name, endpoint, header, payload)
		if err == nil || !retryable || attempt == maxAttempts {
			return raw, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func postOnce(ctx context.Context, client *http.Client, name, endpoint string, header http.Header, payload []byte) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, false, fmt.Errorf("%s: reading response: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		// Gemini and OpenAI-style APIs both report {"error": {"message": "..."}}.
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
		return nil, retryable, fmt.Errorf("%s: HTTP %d: %s", name, resp.StatusCode, firstNonEmpty(e.Error.Message, strings.TrimSpace(string(raw))))
	}
	return raw, false, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
