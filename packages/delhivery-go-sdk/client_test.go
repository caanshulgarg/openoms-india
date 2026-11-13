package delhivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type route func(w http.ResponseWriter, r *http.Request)

func newServer(t *testing.T, routes map[string]route) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token tok123" {
			t.Errorf("Authorization = %q, want %q", got, "Token tok123")
		}
		h, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(" tok123 ", WithBaseURL(srv.URL+"/"), WithHTTPClient(srv.Client()))
}

func TestBaseURLs(t *testing.T) {
	if c := NewClient("t"); c.baseURL != "https://track.delhivery.com" {
		t.Errorf("production base = %q", c.baseURL)
	}
	if c := NewClient("t", WithStaging()); c.baseURL != "https://staging-express.delhivery.com" {
		t.Errorf("staging base = %q", c.baseURL)
	}
}

func TestMissingToken(t *testing.T) {
	c := NewClient("  ")
	if _, err := c.Track(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token error, got %v", err)
	}
}

func sampleCreate() *CreateRequest {
	return &CreateRequest{
		PickupLocation: PickupLocationRef{Name: "Noida WH"},
		Shipments: []Shipment{{
			Name: "Asha & Co", Add: "12 MG Road #4; near park", Pin: "560001", City: "Bengaluru", State: "Karnataka",
			Country: "India", Phone: "9876543210", Order: "ORD-1", PaymentMode: PaymentCOD,
			CODAmount: 998, TotalAmount: 998, ProductsDesc: "Shirt x2", Quantity: 2, Weight: 500,
			Width: 10, Height: 5, Length: 12, ShippingMode: ModeSurface,
		}},
	}
}

func TestCreateShipmentEncodingAndSuccess(t *testing.T) {
	c := newServer(t, map[string]route{
		"POST /api/cmu/create.json": func(w http.ResponseWriter, r *http.Request) {
			if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Errorf("Content-Type = %q", ct)
			}
			raw, _ := io.ReadAll(r.Body)
			if !strings.HasPrefix(string(raw), "data=") && !strings.Contains(string(raw), "format=json") {
				t.Errorf("body is not format=json&data=...: %s", raw)
			}
			form, err := url.ParseQuery(string(raw))
			if err != nil {
				t.Fatalf("body is not form-encoded: %v", err)
			}
			if form.Get("format") != "json" {
				t.Errorf("format = %q", form.Get("format"))
			}
			var data CreateRequest
			if err := json.Unmarshal([]byte(form.Get("data")), &data); err != nil {
				t.Fatalf("data is not json: %v", err)
			}
			s := data.Shipments[0]
			// Special characters must survive the form encoding intact.
			if s.Name != "Asha & Co" || s.Add != "12 MG Road #4; near park" {
				t.Errorf("special characters mangled: %q / %q", s.Name, s.Add)
			}
			if data.PickupLocation.Name != "Noida WH" || s.PaymentMode != "COD" || s.Weight != 500 || s.Order != "ORD-1" {
				t.Errorf("unexpected data: %+v", data)
			}
			var generic map[string]any
			_ = json.Unmarshal([]byte(form.Get("data")), &generic)
			first := generic["shipments"].([]any)[0].(map[string]any)
			if wb, ok := first["waybill"]; !ok || wb != "" {
				t.Errorf("waybill must be sent as empty string for auto-assignment, got %v (present=%v)", wb, ok)
			}
			_, _ = io.WriteString(w, `{"cash_pickups_count":0,"package_count":1,"upload_wbn":"UPL123","replacement_count":0,"pickups_count":0,
			"packages":[{"status":"Success","client":"ACME","sort_code":"BLR/KOR","remarks":[""],"waybill":"1490811234567","cod_amount":998,"payment":"COD","serviceable":true,"refnum":"ORD-1"}],
			"cash_pickups":0,"cod_count":1,"success":true,"prepaid_count":0,"cod_amount":998}`)
		},
	})
	resp, err := c.CreateShipment(context.Background(), sampleCreate())
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success || len(resp.Packages) != 1 || resp.Packages[0].Waybill != "1490811234567" || resp.Packages[0].RefNum != "ORD-1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCreateShipmentFailureShapes(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"package fail with array remarks": {
			`{"success":false,"rmk":"An internal Error has occurred","packages":[{"status":"Fail","waybill":"","refnum":"ORD-1","remarks":["Crashing while saving package due to exception 'ClientWarehouse matching query does not exist.'"]}]}`,
			"ClientWarehouse matching query does not exist",
		},
		"package fail with string remarks": {
			`{"success":false,"packages":[{"status":"Fail","waybill":"","refnum":"ORD-1","remarks":"Duplicate order id"}]}`,
			"Duplicate order id",
		},
		"no packages": {
			`{"success":false,"error":true,"rmk":"format key missing in POST","packages":[]}`,
			"format key missing",
		},
		"success status but no waybill": {
			`{"success":true,"packages":[{"status":"Success","waybill":"","refnum":"ORD-1","remarks":[""]}]}`,
			`status "Success"`,
		},
		"numeric waybill is accepted but second package fails": {
			`{"success":false,"rmk":"partial","packages":[{"status":"Success","waybill":123,"refnum":"A"},{"status":"Fail","waybill":"","refnum":"B","remarks":[]}]}`,
			`order "B": partial`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newServer(t, map[string]route{
				"POST /api/cmu/create.json": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) },
			})
			_, err := c.CreateShipment(context.Background(), sampleCreate())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestCreateShipmentValidation(t *testing.T) {
	c := NewClient("tok")
	if _, err := c.CreateShipment(context.Background(), nil); err == nil {
		t.Error("nil request should fail")
	}
	if _, err := c.CreateShipment(context.Background(), &CreateRequest{PickupLocation: PickupLocationRef{Name: "x"}}); err == nil {
		t.Error("no shipments should fail")
	}
	req := sampleCreate()
	req.PickupLocation.Name = " "
	if _, err := c.CreateShipment(context.Background(), req); err == nil {
		t.Error("missing pickup location should fail")
	}
}

func TestHTTPErrorIsAPIError(t *testing.T) {
	c := newServer(t, map[string]route{
		"GET /api/v1/packages/json/": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"detail":"Invalid token."}`)
		},
	})
	_, err := c.Track(context.Background(), "1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 || apiErr.Message != "Invalid token." {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPackingSlip(t *testing.T) {
	c := newServer(t, map[string]route{
		"GET /api/p/packing_slip": func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("wbns") != "1490811234567" || q.Get("pdf") != "true" || q.Get("pdf_size") != "4R" {
				t.Errorf("unexpected query: %v", q)
			}
			_, _ = io.WriteString(w, `{"packages":[{"wbn":"1490811234567","pdf_download_link":"https://files.example.com/slip.pdf"}],"packages_found":1}`)
		},
	})
	slip, err := c.PackingSlip(context.Background(), "1490811234567", "4R")
	if err != nil || slip.PDFDownloadLink != "https://files.example.com/slip.pdf" || slip.PackagesFound != 1 {
		t.Fatalf("slip=%+v err=%v", slip, err)
	}
	if _, err := c.PackingSlip(context.Background(), "", ""); err == nil {
		t.Error("empty waybill should fail")
	}
}

func TestPackingSlipNotFound(t *testing.T) {
	c := newServer(t, map[string]route{
		"GET /api/p/packing_slip": func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.URL.Query()["pdf_size"]; ok {
				t.Error("pdf_size must be omitted when empty")
			}
			_, _ = io.WriteString(w, `{"packages":[],"packages_found":0}`)
		},
	})
	if _, err := c.PackingSlip(context.Background(), "404", ""); err == nil {
		t.Fatal("expected error when no label is found")
	}
}

func TestTrack(t *testing.T) {
	c := newServer(t, map[string]route{
		"GET /api/v1/packages/json/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("waybill") != "111,222" {
				t.Errorf("waybill = %q", r.URL.Query().Get("waybill"))
			}
			_, _ = io.WriteString(w, `{"ShipmentData":[{"Shipment":{"AWB":"111","ReferenceNo":"ORD-1","Origin":"Noida_WH (Uttar Pradesh)","Destination":"Bengaluru","CODAmount":998,
			"Status":{"Status":"Delivered","StatusLocation":"Bengaluru_Kormngla_D (Karnataka)","StatusDateTime":"2026-11-16T14:02:11.767","RecievedBy":"Asha","StatusType":"DL","StatusCode":"EOD-38","Instructions":"Delivered to consignee"},
			"Scans":[{"ScanDetail":{"ScanDateTime":"2026-11-13T19:20:05.002","ScanType":"UD","Scan":"Manifested","StatusDateTime":"2026-11-13T19:20:05.002","ScannedLocation":"Noida_WH (Uttar Pradesh)","Instructions":"Consignment Manifested","StatusCode":"X-UCI"}},
			         {"ScanDetail":{"ScanDateTime":"2026-11-16T14:02:11","ScanType":"DL","Scan":"Delivered","ScannedLocation":"Bengaluru_Kormngla_D (Karnataka)","Instructions":"Delivered to consignee","StatusCode":"EOD-38"}}]}},
			{"Shipment":{"AWB":222,"Status":{"Status":"In Transit","StatusType":"UD"},"Scans":[]}}]}`)
		},
	})
	ships, err := c.Track(context.Background(), "111", " 222 ", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ships) != 2 || ships[0].AWB != "111" || ships[1].AWB != "222" {
		t.Fatalf("unexpected shipments: %+v", ships)
	}
	s := ships[0]
	if s.Status.StatusType != "DL" || s.Status.Status != "Delivered" || s.Status.ReceivedBy != "Asha" || len(s.Scans) != 2 {
		t.Fatalf("unexpected shipment: %+v", s)
	}
	if s.Scans[0].ScanDetail.Scan != "Manifested" || s.Scans[1].ScanDetail.ScanType != "DL" || s.CODAmount != 998 {
		t.Fatalf("unexpected scans: %+v", s.Scans)
	}
	if _, err := c.Track(context.Background(), " ", ""); err == nil {
		t.Error("no waybills should fail")
	}
}

func TestTrackErrorAndEmpty(t *testing.T) {
	body := `{"Error":"Data does not exists for provided Waybill(s)","Success":false,"rmk":""}`
	c := newServer(t, map[string]route{
		"GET /api/v1/packages/json/": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) },
	})
	if _, err := c.Track(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "does not exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	body = `{"ShipmentData":[]}`
	ships, err := c.Track(context.Background(), "1")
	if err != nil || ships != nil {
		t.Fatalf("empty data should be (nil, nil), got %v, %v", ships, err)
	}
}

func TestCancel(t *testing.T) {
	body := `{"status":true,"waybill":"111","remark":"Shipment has been cancelled.","order_id":"ORD-1"}`
	c := newServer(t, map[string]route{
		"POST /api/p/edit": func(w http.ResponseWriter, r *http.Request) {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"cancellation":"true","waybill":"111"}` {
				t.Errorf("body = %s", raw)
			}
			_, _ = io.WriteString(w, body)
		},
	})
	if err := c.Cancel(context.Background(), "111"); err != nil {
		t.Fatal(err)
	}
	body = `{"status":"Success"}`
	if err := c.Cancel(context.Background(), "111"); err != nil {
		t.Fatalf("string status should be accepted: %v", err)
	}
	body = `{"status":false,"error":"Shipment already delivered"}`
	if err := c.Cancel(context.Background(), "111"); err == nil || !strings.Contains(err.Error(), "already delivered") {
		t.Fatalf("unexpected error: %v", err)
	}
	body = `{}`
	if err := c.Cancel(context.Background(), "111"); err == nil {
		t.Fatal("unconfirmed cancellation must be an error")
	}
	if err := c.Cancel(context.Background(), " "); err == nil {
		t.Fatal("empty waybill should fail")
	}
}

func TestCreatePickupRequest(t *testing.T) {
	body := `{"pickup_location_name":"Noida WH","client_name":"ACME","pickup_time":"14:00:00","pickup_id":8172635,"incoming_center_name":"Noida_Sector63_PC (Uttar Pradesh)","expected_package_count":3,"pickup_date":"2026-11-14"}`
	c := newServer(t, map[string]route{
		"POST /fm/request/new/": func(w http.ResponseWriter, r *http.Request) {
			var got PickupRequest
			_ = json.NewDecoder(r.Body).Decode(&got)
			if got.PickupLocation != "Noida WH" || got.PickupDate != "2026-11-14" || got.PickupTime != "14:00:00" || got.ExpectedPackageCount != 1 {
				t.Errorf("unexpected request: %+v", got)
			}
			_, _ = io.WriteString(w, body)
		},
	})
	req := PickupRequest{PickupLocation: "Noida WH", PickupDate: "2026-11-14", PickupTime: "14:00:00"}
	resp, err := c.CreatePickupRequest(context.Background(), req)
	if err != nil || resp.PickupID != "8172635" || !strings.HasPrefix(resp.IncomingCenterName, "Noida") {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	body = `{"pr_exist":true,"error":"Pickup request already exists for this date","success":false}`
	if _, err := c.CreatePickupRequest(context.Background(), req); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := c.CreatePickupRequest(context.Background(), PickupRequest{PickupLocation: "x"}); err == nil {
		t.Fatal("missing date/time should fail")
	}
}

func TestShippingCharges(t *testing.T) {
	body := `[{"zone":"D","charge_COD":41.3,"gross_amount":95.0,"total_amount":160.83,"charged_weight":500,"status":"Delivered","tax_data":{"IGST":24.53}}]`
	var gotQuery url.Values
	c := newServer(t, map[string]route{
		"GET /api/kinko/v1/invoice/charges/.json": func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query()
			_, _ = io.WriteString(w, body)
		},
	})
	charges, err := c.ShippingCharges(context.Background(), ChargesRequest{
		Mode: ModeSurface, OriginPin: "201301", DestinationPin: "560001", WeightGrams: 500, COD: true, CODAmount: 998,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 1 || charges[0].TotalAmount != 160.83 || charges[0].ChargeCOD != 41.3 || charges[0].Zone != "D" {
		t.Fatalf("unexpected charges: %+v", charges)
	}
	want := map[string]string{"md": "S", "ss": "Delivered", "o_pin": "201301", "d_pin": "560001", "cgm": "500", "pt": "COD", "cod": "998"}
	for k, v := range want {
		if gotQuery.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, gotQuery.Get(k), v)
		}
	}

	if _, err := c.ShippingCharges(context.Background(), ChargesRequest{Mode: ModeExpress, OriginPin: "1", DestinationPin: "2", WeightGrams: 100}); err != nil {
		t.Fatal(err)
	}
	if gotQuery.Get("md") != "E" || gotQuery.Get("pt") != "Pre-paid" || gotQuery.Has("cod") {
		t.Errorf("unexpected prepaid/express query: %v", gotQuery)
	}

	body = `{"error":"Invalid pincode"}`
	if _, err := c.ShippingCharges(context.Background(), ChargesRequest{OriginPin: "1", DestinationPin: "2"}); err == nil {
		t.Fatal("non-array body must be an error")
	}
	if _, err := c.ShippingCharges(context.Background(), ChargesRequest{}); err == nil {
		t.Fatal("missing pins should fail")
	}
}

func TestPincodeServiceability(t *testing.T) {
	body := `{"delivery_codes":[{"postal_code":{"pin":560001,"city":"Bengaluru","district":"Bangalore","state_code":"KA","pre_paid":"Y","cod":"N","pickup":"Y","repl":"y","is_oda":"N","remarks":""}}]}`
	c := newServer(t, map[string]route{
		"GET /c/api/pin-codes/json/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("filter_codes") == "" {
				t.Error("filter_codes missing")
			}
			_, _ = io.WriteString(w, body)
		},
	})
	info, err := c.PincodeServiceability(context.Background(), "560001")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || !info.Prepaid || info.COD || !info.Pickup || !info.Replacement || info.ODA || info.StateCode != "KA" || info.Pin != "560001" {
		t.Fatalf("unexpected info: %+v", info)
	}
	body = `{"delivery_codes":[]}`
	info, err = c.PincodeServiceability(context.Background(), "999999")
	if err != nil || info != nil {
		t.Fatalf("unserviced pin should be (nil, nil), got %+v, %v", info, err)
	}
	if _, err := c.PincodeServiceability(context.Background(), ""); err == nil {
		t.Fatal("empty pin should fail")
	}
}

func TestDownload(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization header must not be sent to file storage")
		}
		if r.URL.Path == "/empty.pdf" {
			return
		}
		if r.URL.Path == "/gone.pdf" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "%PDF-1.4 fake")
	}))
	defer srv.Close()
	c := NewClient("tok", WithHTTPClient(srv.Client()))
	data, err := c.Download(context.Background(), srv.URL+"/slip.pdf?X-Amz-Signature=abc")
	if err != nil || string(data) != "%PDF-1.4 fake" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	for _, u := range []string{srv.URL + "/empty.pdf", srv.URL + "/gone.pdf", "http://files.example.com/a.pdf", "file:///etc/passwd"} {
		if _, err := c.Download(context.Background(), u); err == nil {
			t.Errorf("Download(%q) should fail", u)
		}
	}
}

func TestParseTime(t *testing.T) {
	got := ParseTime("2026-11-16T14:02:11.767")
	want := time.Date(2026, 11, 16, 8, 32, 11, 767000000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("zone-less timestamp must be read as IST: got %v, want %v", got.UTC(), want)
	}
	if !ParseTime("2026-11-16T14:02:11").Equal(time.Date(2026, 11, 16, 8, 32, 11, 0, time.UTC)) {
		t.Error("no-fraction layout failed")
	}
	if !ParseTime("2026-11-16 14:02:11").Equal(time.Date(2026, 11, 16, 8, 32, 11, 0, time.UTC)) {
		t.Error("space layout failed")
	}
	if !ParseTime("2026-11-16T14:02:11+05:30").Equal(time.Date(2026, 11, 16, 8, 32, 11, 0, time.UTC)) {
		t.Error("RFC3339 failed")
	}
	if !ParseTime("").IsZero() || !ParseTime("yesterday").IsZero() {
		t.Error("unparseable input must give zero time")
	}
}

func TestMapStatus(t *testing.T) {
	cases := []struct {
		typ, status, want string
		ok                bool
	}{
		{"UD", "Manifested", "", false},
		{"UD", "Not Picked", "", false},
		{"UD", "In Transit", "in_transit", true},
		{"ud", "in  transit", "in_transit", true},
		{"UD", "Pending", "in_transit", true},
		{"UD", "Dispatched", "out_for_delivery", true},
		{"DL", "Delivered", "delivered", true},
		{"DL", "RTO", "returned", true},
		{"DL", "DTO", "", false},
		{"RT", "In Transit", "", false},
		{"RT", "Dispatched", "", false},
		{"RT", "Delivered", "", false},
		{"CN", "Cancelled", "", false},
		{"PP", "Open", "", false},
		{"LT", "Lost", "failed", true},
		{"", "Delivered", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		got, ok := MapStatus(tc.typ, tc.status)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MapStatus(%q, %q) = (%q, %v), want (%q, %v)", tc.typ, tc.status, got, ok, tc.want, tc.ok)
		}
		gotKey, okKey := MapStatusKey(StatusKey(tc.typ, tc.status))
		if tc.typ != "" && (gotKey != tc.want || okKey != tc.ok) {
			t.Errorf("MapStatusKey(%q) = (%q, %v), want (%q, %v)", StatusKey(tc.typ, tc.status), gotKey, okKey, tc.want, tc.ok)
		}
	}
	if typ, st := SplitStatusKey("UD:In Transit"); typ != "UD" || st != "In Transit" {
		t.Errorf("SplitStatusKey = %q, %q", typ, st)
	}
	if typ, st := SplitStatusKey("Delivered"); typ != "" || st != "Delivered" {
		t.Errorf("SplitStatusKey without type = %q, %q", typ, st)
	}
}
