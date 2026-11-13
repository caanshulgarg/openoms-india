// Package delhivery provides a client for the Delhivery Express (B2C) API.
//
// Hosts:
//
//	production: https://track.delhivery.com
//	staging:    https://staging-express.delhivery.com
//
// Authentication: every request carries "Authorization: Token <api token>".
// The token is issued per client account by Delhivery (Delhivery One ->
// Settings -> API Setup); staging and production use different tokens.
//
// Typical forward-shipment flow:
//
//	c := delhivery.NewClient(token)
//	res, _ := c.CreateShipment(ctx, &delhivery.CreateRequest{...}) // waybill auto-assigned
//	slip, _ := c.PackingSlip(ctx, res.Packages[0].Waybill, "")      // label PDF link
//	pdf, _ := c.Download(ctx, slip.PDFDownloadLink)
//	_, _ = c.CreatePickupRequest(ctx, delhivery.PickupRequest{...})
//	track, _ := c.Track(ctx, res.Packages[0].Waybill)
package delhivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	productionBaseURL = "https://track.delhivery.com"
	stagingBaseURL    = "https://staging-express.delhivery.com"
	maxBodyBytes      = 10 << 20 // 10 MB for JSON responses
	maxFileBytes      = 25 << 20 // 25 MB for label PDFs
)

// Client is a Delhivery Express API client. It is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client used for API requests.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		if c != nil {
			cl.httpClient = c
		}
	}
}

// WithStaging points the client at the Delhivery staging environment.
func WithStaging() Option {
	return func(cl *Client) { cl.baseURL = stagingBaseURL }
}

// WithBaseURL overrides the API base URL (useful for tests).
func WithBaseURL(u string) Option {
	return func(cl *Client) {
		if u != "" {
			cl.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// NewClient creates a Delhivery client for the given API token.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		httpClient: http.DefaultClient,
		baseURL:    productionBaseURL,
		token:      strings.TrimSpace(token),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is returned for any non-2xx Delhivery response.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("delhivery: api error %d: %s", e.StatusCode, msg)
}

func parseAPIError(status int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: status}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil {
		for _, key := range []string{"detail", "error", "rmk", "message", "remark"} {
			if txt := rawToText(raw[key]); txt != "" {
				apiErr.Message = txt
				return apiErr
			}
		}
	}
	apiErr.Message = truncate(strings.TrimSpace(string(body)), 300)
	return apiErr
}

// rawToText renders a JSON value that may be a string, an array of strings or
// an arbitrary value as readable text.
func rawToText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			if item = strings.TrimSpace(item); item != "" {
				parts = append(parts, item)
			}
		}
		return strings.Join(parts, "; ")
	}
	return truncate(string(raw), 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// do sends a request and decodes a JSON response into out (when non-nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, out any) error {
	if c.token == "" {
		return errors.New("delhivery: api token is required")
	}
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("delhivery: build request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delhivery: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("delhivery: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp.StatusCode, respBody)
	}
	if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("delhivery: decode %s %s response: %w", method, path, err)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, "", out)
}

func (c *Client) postJSON(ctx context.Context, path string, payload, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("delhivery: marshal request: %w", err)
	}
	return c.do(ctx, http.MethodPost, path, nil, bytes.NewReader(b), "application/json", out)
}

// Download fetches a label PDF from a link returned by PackingSlip. The link
// points at Delhivery's file storage (pre-signed), so no Authorization header
// is sent. Only https URLs are accepted.
func (c *Client) Download(ctx context.Context, fileURL string) ([]byte, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return nil, fmt.Errorf("delhivery: parse file url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("delhivery: refusing to download non-https file url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("delhivery: build download request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("delhivery: download file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("delhivery: download file: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("delhivery: read file: %w", err)
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("delhivery: file exceeds %d bytes", maxFileBytes)
	}
	if len(data) == 0 {
		return nil, errors.New("delhivery: downloaded file is empty")
	}
	return data, nil
}
