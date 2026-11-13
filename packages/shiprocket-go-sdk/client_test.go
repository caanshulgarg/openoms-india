package shiprocket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI is a minimal Shiprocket stand-in that counts logins.
type fakeAPI struct {
	t      *testing.T
	logins atomic.Int32
	token  atomic.Value // current valid token (string)
	routes map[string]http.HandlerFunc
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	f := &fakeAPI{t: t, routes: map[string]http.HandlerFunc{}}
	f.token.Store("tok-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/login" {
			var creds map[string]string
			_ = json.NewDecoder(r.Body).Decode(&creds)
			if creds["email"] != "api@example.com" || creds["password"] != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"message":"Invalid email and password combination","status_code":401}`)
				return
			}
			f.logins.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"token": f.token.Load()})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.token.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Token has expired","status_code":401}`)
			return
		}
		h, ok := f.routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func newTestClient(srv *httptest.Server, opts ...Option) *Client {
	opts = append([]Option{WithBaseURL(srv.URL), WithHTTPClient(srv.Client())}, opts...)
	return NewClient("api@example.com", "secret", opts...)
}

func TestLoginIsCachedAcrossCalls(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /settings/company/pickup"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"shipping_address":[{"id":1,"pickup_location":"Noida WH","pin_code":"201301","phone":9999999999,"primary_location":1}]}}`)
	}
	c := newTestClient(srv)

	for i := 0; i < 3; i++ {
		locs, err := c.PickupLocations(context.Background())
		if err != nil {
			t.Fatalf("PickupLocations: %v", err)
		}
		if len(locs) != 1 || locs[0].PickupLocation != "Noida WH" || locs[0].Phone != "9999999999" || locs[0].ID != "1" {
			t.Fatalf("unexpected locations: %+v", locs)
		}
	}
	if got := f.logins.Load(); got != 1 {
		t.Fatalf("logins = %d, want 1", got)
	}
}

func TestTokenRefreshesAfterTTL(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /settings/company/pickup"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"shipping_address":[]}}`)
	}
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	c := newTestClient(srv, WithNow(func() time.Time { return now }))

	if _, err := c.PickupLocations(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(tokenTTL + time.Minute)
	if _, err := c.PickupLocations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.logins.Load(); got != 2 {
		t.Fatalf("logins = %d, want 2", got)
	}
}

func TestReLoginOnceOn401(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /settings/company/pickup"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"shipping_address":[]}}`)
	}
	c := newTestClient(srv)
	if _, err := c.PickupLocations(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Server-side token rotation: the cached token is now rejected.
	f.token.Store("tok-2")
	if _, err := c.PickupLocations(context.Background()); err != nil {
		t.Fatalf("expected transparent re-login, got %v", err)
	}
	if got := f.logins.Load(); got != 2 {
		t.Fatalf("logins = %d, want 2", got)
	}
}

func TestLoginFailureSurfacesAPIError(t *testing.T) {
	_, srv := newFakeAPI(t)
	c := NewClient("api@example.com", "wrong", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.PickupLocations(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 APIError, got %v", err)
	}
	if !strings.Contains(apiErr.Error(), "Invalid email") {
		t.Fatalf("error lacks server message: %v", apiErr)
	}
}

func TestMissingCredentials(t *testing.T) {
	c := NewClient("", "")
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestValidationErrorFieldsAreFlattened(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /orders/create/adhoc"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Oops! Invalid Data.","errors":{"billing_phone":["The billing phone must be 10 digits."],"pickup_location":["Wrong Pickup location entered."]},"status_code":422}`)
	}
	c := newTestClient(srv)
	_, err := c.CreateOrder(context.Background(), &CreateOrderRequest{OrderID: "A1"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}
	msg := apiErr.Error()
	for _, want := range []string{"422", "Invalid Data", "billing_phone: The billing phone must be 10 digits.", "pickup_location: Wrong Pickup location entered."} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestDownloadRejectsNonHTTPS(t *testing.T) {
	c := NewClient("a", "b")
	for _, u := range []string{"http://files.example.com/label.pdf", "file:///etc/passwd", "ftp://x/y", "://bad"} {
		if _, err := c.Download(context.Background(), u); err == nil {
			t.Errorf("Download(%q) should fail", u)
		}
	}
}

func TestDownloadDoesNotSendAuthorization(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization header must not be sent to file storage")
		}
		_, _ = io.WriteString(w, "%PDF-1.4 fake")
	}))
	defer srv.Close()
	c := NewClient("a", "b", WithHTTPClient(srv.Client()))
	data, err := c.Download(context.Background(), srv.URL+"/label.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "%PDF-1.4 fake" {
		t.Fatalf("unexpected body %q", data)
	}
}

func TestDownloadErrors(t *testing.T) {
	status := http.StatusForbidden
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := NewClient("a", "b", WithHTTPClient(srv.Client()))
	if _, err := c.Download(context.Background(), srv.URL+"/x.pdf"); err == nil {
		t.Fatal("expected error on 403")
	}
	status = http.StatusOK
	if _, err := c.Download(context.Background(), srv.URL+"/x.pdf"); err == nil {
		t.Fatal("expected error on empty file")
	}
}

func TestFlexTypes(t *testing.T) {
	var v struct {
		A FlexString `json:"a"`
		B FlexString `json:"b"`
		C FlexString `json:"c"`
		D FlexString `json:"d"`
		E FlexFloat  `json:"e"`
		F FlexFloat  `json:"f"`
		G FlexFloat  `json:"g"`
		H FlexFloat  `json:"h"`
	}
	in := `{"a":12345,"b":"AWB9","c":null,"d":{"x":1},"e":"84.5","f":61,"g":null,"h":"n/a"}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != "12345" || v.B != "AWB9" || v.C != "" || v.D != "" {
		t.Fatalf("FlexString decode wrong: %+v", v)
	}
	if v.E != 84.5 || v.F != 61 || v.G != 0 || v.H != 0 {
		t.Fatalf("FlexFloat decode wrong: %+v", v)
	}
}
