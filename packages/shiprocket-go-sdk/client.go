// Package shiprocket provides a client for the Shiprocket external API
// (https://apiv2.shiprocket.in/v1/external), the Indian multi-courier
// shipping aggregator.
//
// Authentication: an API user (Shiprocket panel -> Settings -> API -> Configure)
// logs in with email + password at POST /auth/login and receives a JWT that is
// sent as "Authorization: Bearer <token>" on every other call. Shiprocket
// documents the token as valid for 10 days; this client refreshes it every
// 24 hours and transparently re-logs in once when a call returns HTTP 401.
package shiprocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://apiv2.shiprocket.in/v1/external"
	maxBodyBytes   = 10 << 20 // 10 MB for JSON responses
	maxFileBytes   = 25 << 20 // 25 MB for label / manifest PDFs
	// tokenTTL is deliberately far below the documented 10-day validity.
	tokenTTL = 24 * time.Hour
)

// Client is a Shiprocket API client. It is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	email      string
	password   string
	now        func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time
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

// WithBaseURL overrides the API base URL (useful for tests).
func WithBaseURL(u string) Option {
	return func(cl *Client) {
		if u != "" {
			cl.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithNow overrides the clock (useful for tests).
func WithNow(fn func() time.Time) Option {
	return func(cl *Client) {
		if fn != nil {
			cl.now = fn
		}
	}
}

// NewClient creates a Shiprocket client for the given API user.
func NewClient(email, password string, opts ...Option) *Client {
	c := &Client{
		httpClient: http.DefaultClient,
		baseURL:    defaultBaseURL,
		email:      email,
		password:   password,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is returned for any non-2xx Shiprocket response.
type APIError struct {
	StatusCode int
	Message    string
	// Fields holds per-field validation messages (HTTP 422), flattened.
	Fields map[string]string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if len(e.Fields) == 0 {
		return fmt.Sprintf("shiprocket: api error %d: %s", e.StatusCode, msg)
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return fmt.Sprintf("shiprocket: api error %d: %s (%s)", e.StatusCode, msg, strings.Join(parts, "; "))
}

// parseAPIError builds an APIError from a raw error body.
func parseAPIError(status int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: status}
	var raw struct {
		Message json.RawMessage            `json:"message"`
		Errors  map[string]json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		apiErr.Message = truncate(strings.TrimSpace(string(body)), 300)
		return apiErr
	}
	apiErr.Message = rawToText(raw.Message)
	if len(raw.Errors) > 0 {
		apiErr.Fields = make(map[string]string, len(raw.Errors))
		for k, v := range raw.Errors {
			apiErr.Fields[k] = rawToText(v)
		}
	}
	return apiErr
}

// rawToText renders a JSON value that may be a string, an array of strings or
// an arbitrary object as readable text.
func rawToText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, ", ")
	}
	return truncate(string(raw), 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Login forces a fresh login and caches the token.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	if c.email == "" || c.password == "" {
		return errors.New("shiprocket: email and password are required")
	}
	payload, err := json.Marshal(map[string]string{"email": c.email, "password": c.password})
	if err != nil {
		return fmt.Errorf("shiprocket: marshal login: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/login", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("shiprocket: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("shiprocket: login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("shiprocket: read login response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("shiprocket: decode login response: %w", err)
	}
	if out.Token == "" {
		return errors.New("shiprocket: login succeeded but no token was returned")
	}
	c.token = out.Token
	c.tokenExp = c.now().Add(tokenTTL)
	return nil
}

// authToken returns a valid cached token, logging in when needed.
func (c *Client) authToken(ctx context.Context, forceRefresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if forceRefresh || c.token == "" || !c.now().Before(c.tokenExp) {
		if err := c.loginLocked(ctx); err != nil {
			return "", err
		}
	}
	return c.token, nil
}

// do performs an authenticated JSON request. On HTTP 401 it logs in again and
// retries exactly once.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, payload, out any) error {
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("shiprocket: marshal request: %w", err)
		}
		body = b
	}

	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.authToken(ctx, attempt > 0)
		if err != nil {
			return err
		}

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
		if err != nil {
			return fmt.Errorf("shiprocket: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		status, respBody, err := c.send(req)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			continue // token expired or revoked: re-login once
		}
		if status < 200 || status > 299 {
			return parseAPIError(status, respBody)
		}
		if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("shiprocket: decode %s %s response: %w", method, path, err)
		}
		return nil
	}
	return &APIError{StatusCode: http.StatusUnauthorized, Message: "unauthorized after re-login"}
}

func (c *Client) send(req *http.Request) (int, []byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("shiprocket: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("shiprocket: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// Download fetches a document (label, manifest or invoice PDF) from a URL
// returned by Shiprocket. These URLs point at Shiprocket's file storage, so no
// Authorization header is sent. Only https URLs are accepted.
func (c *Client) Download(ctx context.Context, fileURL string) ([]byte, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: parse file url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("shiprocket: refusing to download non-https file url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: build download request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: download file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("shiprocket: download file: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("shiprocket: read file: %w", err)
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("shiprocket: file exceeds %d bytes", maxFileBytes)
	}
	if len(data) == 0 {
		return nil, errors.New("shiprocket: downloaded file is empty")
	}
	return data, nil
}
