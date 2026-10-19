// Package woo is a minimal client for the WooCommerce REST API v3.
package woo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiPrefix = "/wp-json/wc/v3"
	// WooCommerce caps per_page at 100.
	maxPerPage = 100
)

// Client talks to one store, authenticating with a WordPress application password.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

func New(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// APIError is the error body WordPress returns for non-2xx responses.
type APIError struct {
	StatusCode int
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("woocommerce: %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// do sends a request to path (relative to the site root) and decodes a JSON response into out.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) (http.Header, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if json.Unmarshal(raw, apiErr) != nil || apiErr.Message == "" {
			apiErr.Message = strings.TrimSpace(string(raw))
		}
		return nil, apiErr
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return nil, fmt.Errorf("woocommerce: decode %s %s: %w", method, path, err)
		}
	}
	// Drain what the decoder left so the keep-alive connection is reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Header, nil
}

// listAll walks every page of a collection endpoint, following X-WP-TotalPages.
func listAll[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("per_page", strconv.Itoa(maxPerPage))
	// Default newest-first paging repeats rows when new ones arrive mid-walk; ascending IDs only append.
	q.Set("orderby", "id")
	q.Set("order", "asc")

	var all []T
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		var batch []T
		h, err := c.do(ctx, http.MethodGet, apiPrefix+path, q, nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)

		totalPages, err := strconv.Atoi(h.Get("X-WP-TotalPages"))
		if err != nil {
			return nil, fmt.Errorf("woocommerce: GET %s: bad X-WP-TotalPages %q", path, h.Get("X-WP-TotalPages"))
		}
		if page >= totalPages {
			return all, nil
		}
	}
}

// Time is a WooCommerce *_gmt timestamp, which is UTC but carries no zone suffix.
type Time struct{ time.Time }

const wooTimeLayout = "2006-01-02T15:04:05"

func (t *Time) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	parsed, err := time.ParseInLocation(wooTimeLayout, s, time.UTC)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}
