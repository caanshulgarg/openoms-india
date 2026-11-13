package shiprocket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return m
}

func TestCreateOrder(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /orders/create/adhoc"] = func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		body := decodeBody(t, r)
		if body["order_id"] != "ORD-1" || body["payment_method"] != "COD" || body["pickup_location"] != "Noida WH" {
			t.Errorf("unexpected body: %v", body)
		}
		if body["shipping_is_billing"] != true {
			t.Errorf("shipping_is_billing must be sent as true, got %v", body["shipping_is_billing"])
		}
		items := body["order_items"].([]any)
		first := items[0].(map[string]any)
		if first["sku"] != "SKU1" || first["units"].(float64) != 2 || first["selling_price"].(float64) != 499 {
			t.Errorf("unexpected item: %v", first)
		}
		_, _ = io.WriteString(w, `{"order_id":16161616,"shipment_id":15151515,"status":"NEW","status_code":1,"onboarding_completed_now":0,"awb_code":null,"courier_company_id":null,"courier_name":null}`)
	}
	c := newTestClient(srv)
	resp, err := c.CreateOrder(context.Background(), &CreateOrderRequest{
		OrderID: "ORD-1", OrderDate: "2026-11-13 13:00", PickupLocation: "Noida WH",
		BillingCustomerName: "Asha", BillingAddress: "12 MG Road", BillingCity: "Bengaluru",
		BillingPincode: "560001", BillingState: "Karnataka", BillingCountry: "India", BillingPhone: "9876543210",
		ShippingIsBilling: true, PaymentMethod: "COD", SubTotal: 998,
		OrderItems: []OrderItem{{Name: "Shirt", SKU: "SKU1", Units: 2, SellingPrice: 499}},
		Length:     10, Breadth: 10, Height: 5, Weight: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OrderID != "16161616" || resp.ShipmentID != "15151515" || resp.AWBCode != "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCreateOrderWithoutIDsIsAnError(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /orders/create/adhoc"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"CANCELED","status_code":5}`)
	}
	if _, err := newTestClient(srv).CreateOrder(context.Background(), &CreateOrderRequest{OrderID: "x"}); err == nil {
		t.Fatal("expected error when ids are missing")
	}
	if _, err := newTestClient(srv).CreateOrder(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestAssignAWB(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /courier/assign/awb"] = func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["shipment_id"] != "15151515" {
			t.Errorf("shipment_id = %v", body["shipment_id"])
		}
		if body["courier_id"] != "10" {
			t.Errorf("courier_id = %v", body["courier_id"])
		}
		_, _ = io.WriteString(w, `{"awb_assign_status":1,"response":{"data":{"courier_company_id":10,"awb_code":"1091188857722","courier_name":"Delhivery Surface","routing_code":"DEL/ANA","shipment_id":15151515}}}`)
	}
	awb, err := newTestClient(srv).AssignAWB(context.Background(), "15151515", "10")
	if err != nil {
		t.Fatal(err)
	}
	if awb.AWBCode != "1091188857722" || awb.CourierCompanyID != "10" || awb.CourierName != "Delhivery Surface" {
		t.Fatalf("unexpected awb: %+v", awb)
	}
}

func TestAssignAWBAutoCourierOmitsCourierID(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /courier/assign/awb"] = func(w http.ResponseWriter, r *http.Request) {
		if _, ok := decodeBody(t, r)["courier_id"]; ok {
			t.Error("courier_id must be omitted for auto-assignment")
		}
		_, _ = io.WriteString(w, `{"awb_assign_status":1,"response":{"data":{"awb_code":"X1","courier_company_id":"54","courier_name":"Bluedart"}}}`)
	}
	if _, err := newTestClient(srv).AssignAWB(context.Background(), "1", ""); err != nil {
		t.Fatal(err)
	}
}

func TestAssignAWBFailureShapes(t *testing.T) {
	cases := map[string]string{
		"error in data":      `{"awb_assign_status":0,"response":{"data":{"awb_assign_error":"Recharge your wallet"}}}`,
		"message":            `{"awb_assign_status":0,"message":"Courier not serviceable","response":""}`,
		"string response":    `{"awb_assign_status":0,"response":"AWB is already assigned"}`,
		"status ok, no awb":  `{"awb_assign_status":1,"response":{"data":{"awb_code":""}}}`,
		"empty object":       `{}`,
		"unexpected message": `{"awb_assign_status":0,"message":{"shipment_id":["invalid"]}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			f, srv := newFakeAPI(t)
			f.routes["POST /courier/assign/awb"] = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, payload)
			}
			_, err := newTestClient(srv).AssignAWB(context.Background(), "1", "")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "assign awb failed") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestGeneratePickupLabelManifestInvoice(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /courier/generate/pickup"] = func(w http.ResponseWriter, r *http.Request) {
		ids := decodeBody(t, r)["shipment_id"].([]any)
		if len(ids) != 1 || ids[0] != "15" {
			t.Errorf("shipment_id = %v", ids)
		}
		_, _ = io.WriteString(w, `{"pickup_status":1,"response":{"pickup_scheduled_date":"2026-11-14 10:00:00","pickup_token_number":"Reference No: 194_BIGFOOT 1966840_11122019","status":3}}`)
	}
	f.routes["POST /courier/generate/label"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"label_created":1,"label_url":"https://files.example.com/label.pdf","response":"Label has been created and uploaded successfully!","not_created":[]}`)
	}
	f.routes["POST /manifests/generate"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":1,"manifest_url":"https://files.example.com/manifest.pdf"}`)
	}
	f.routes["POST /manifests/print"] = func(w http.ResponseWriter, r *http.Request) {
		if _, ok := decodeBody(t, r)["order_ids"]; !ok {
			t.Error("order_ids missing")
		}
		_, _ = io.WriteString(w, `{"manifest_url":"https://files.example.com/manifest2.pdf"}`)
	}
	f.routes["POST /orders/print/invoice"] = func(w http.ResponseWriter, r *http.Request) {
		if _, ok := decodeBody(t, r)["ids"]; !ok {
			t.Error("ids missing")
		}
		_, _ = io.WriteString(w, `{"is_invoice_created":true,"invoice_url":"https://files.example.com/inv.pdf","not_created":[]}`)
	}
	c := newTestClient(srv)
	ctx := context.Background()

	p, err := c.GeneratePickup(ctx, "15")
	if err != nil || p.PickupStatus != 1 || p.PickupScheduledDate != "2026-11-14 10:00:00" || p.PickupTokenNumber == "" {
		t.Fatalf("pickup: %+v err=%v", p, err)
	}
	if u, err := c.GenerateLabel(ctx, "15"); err != nil || u != "https://files.example.com/label.pdf" {
		t.Fatalf("label: %q err=%v", u, err)
	}
	if u, err := c.GenerateManifest(ctx, "15"); err != nil || u != "https://files.example.com/manifest.pdf" {
		t.Fatalf("manifest: %q err=%v", u, err)
	}
	if u, err := c.PrintManifest(ctx, "16"); err != nil || u != "https://files.example.com/manifest2.pdf" {
		t.Fatalf("print manifest: %q err=%v", u, err)
	}
	if u, err := c.PrintInvoice(ctx, "16"); err != nil || u != "https://files.example.com/inv.pdf" {
		t.Fatalf("invoice: %q err=%v", u, err)
	}

	for name, fn := range map[string]func() error{
		"label":    func() error { _, err := c.GenerateLabel(ctx); return err },
		"manifest": func() error { _, err := c.GenerateManifest(ctx); return err },
		"print":    func() error { _, err := c.PrintManifest(ctx); return err },
		"invoice":  func() error { _, err := c.PrintInvoice(ctx); return err },
		"cancel":   func() error { return c.CancelOrders(ctx) },
	} {
		if fn() == nil {
			t.Errorf("%s: expected error for empty id list", name)
		}
	}
}

func TestGenerateLabelWithoutURLFails(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /courier/generate/label"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"label_created":0,"response":"AWB not assigned","not_created":[15]}`)
	}
	_, err := newTestClient(srv).GenerateLabel(context.Background(), "15")
	if err == nil || !strings.Contains(err.Error(), "AWB not assigned") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTrackByAWB(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /courier/track/awb/1091188857722"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"tracking_data":{"track_status":1,"shipment_status":7,
		"shipment_track":[{"id":1,"awb_code":"1091188857722","shipment_id":15151515,"order_id":16161616,"current_status":"Delivered","delivered_date":"2026-11-16 14:02:00","destination":"Bengaluru","origin":"Noida","edd":null}],
		"shipment_track_activities":[
			{"date":"2026-11-16 14:02:00","status":"000-T-EOD","activity":"Delivered to consignee","location":"Bengaluru","sr-status":7,"sr-status-label":"DELIVERED"},
			{"date":"2026-11-14 09:10:00","status":"X-PPOM","activity":"Shipment picked up","location":"Noida","sr-status":"42","sr-status-label":"PICKED UP"}],
		"track_url":"https://shiprocket.co/tracking/1091188857722","etd":"2026-11-16 20:00:00"}}`)
	}
	td, err := newTestClient(srv).TrackByAWB(context.Background(), " 1091188857722 ")
	if err != nil {
		t.Fatal(err)
	}
	if td.TrackStatus != 1 || td.ShipmentStatus != "7" || len(td.Activities) != 2 {
		t.Fatalf("unexpected tracking: %+v", td)
	}
	if td.Activities[0].SRStatusLabel != "DELIVERED" || td.Activities[1].SRStatus != "42" {
		t.Fatalf("unexpected activities: %+v", td.Activities)
	}
	if td.ShipmentTrack[0].CurrentStatus != "Delivered" || td.ShipmentTrack[0].ShipmentID != "15151515" {
		t.Fatalf("unexpected shipment track: %+v", td.ShipmentTrack)
	}
	if _, err := newTestClient(srv).TrackByAWB(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty awb")
	}
}

func TestTrackWithNullActivities(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /courier/track/awb/NEW1"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"tracking_data":{"track_status":0,"shipment_status":0,"shipment_track":[{"awb_code":"","current_status":""}],"shipment_track_activities":null,"track_url":"","error":"Aahh! There is no activities found in our DB. Please have some patience it will be updated soon."}}`)
	}
	td, err := newTestClient(srv).TrackByAWB(context.Background(), "NEW1")
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Activities) != 0 || td.Error == "" {
		t.Fatalf("unexpected: %+v", td)
	}
}

func TestCancelOrdersSendsNumericIDs(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["POST /orders/cancel"] = func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"ids":[16161616,"abc"]}` {
			t.Errorf("body = %s", raw)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	if err := newTestClient(srv).CancelOrders(context.Background(), "16161616", "abc"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceability(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /courier/serviceability/"] = func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("pickup_postcode") != "201301" || q.Get("delivery_postcode") != "560001" || q.Get("weight") != "0.5" || q.Get("cod") != "1" || q.Get("declared_value") != "998" {
			t.Errorf("unexpected query: %v", q)
		}
		if q.Get("length") != "10" || q.Get("breadth") != "12" || q.Get("height") != "5" {
			t.Errorf("dimensions missing: %v", q)
		}
		_, _ = io.WriteString(w, `{"status":200,"data":{"recommended_courier_company_id":10,"available_courier_companies":[
			{"courier_company_id":10,"courier_name":"Delhivery Surface","rate":84.5,"freight_charge":61,"cod_charges":23.5,"estimated_delivery_days":"4","etd":"Nov 17, 2026","rating":4.2,"is_surface":true},
			{"courier_company_id":"54","courier_name":"Bluedart Air","rate":"132","freight_charge":"110","cod_charges":"22","estimated_delivery_days":"2","etd":"Nov 15, 2026","rating":"4.6","is_surface":false}]}}`)
	}
	resp, err := newTestClient(srv).Serviceability(context.Background(), ServiceabilityRequest{
		PickupPostcode: "201301", DeliveryPostcode: "560001", WeightKg: 0.5, COD: true, DeclaredValue: 998,
		LengthCm: 10, BreadthCm: 12, HeightCm: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RecommendedCourierID != "10" || len(resp.Couriers) != 2 {
		t.Fatalf("unexpected: %+v", resp)
	}
	if resp.Couriers[0].Rate != 84.5 || resp.Couriers[1].Rate != 132 || resp.Couriers[1].CourierCompanyID != "54" || resp.Couriers[1].EstimatedDeliveryDays != "2" {
		t.Fatalf("unexpected couriers: %+v", resp.Couriers)
	}
}

func TestServiceabilityNoCourierIs404NotError(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.routes["GET /courier/serviceability/"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cod") != "0" {
			t.Errorf("cod = %q, want 0", r.URL.Query().Get("cod"))
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":404,"message":"Delivery postcode not serviceable"}`)
	}
	resp, err := newTestClient(srv).Serviceability(context.Background(), ServiceabilityRequest{PickupPostcode: "201301", DeliveryPostcode: "999999", WeightKg: 1})
	if err != nil {
		t.Fatalf("404 should mean no couriers, got %v", err)
	}
	if len(resp.Couriers) != 0 {
		t.Fatalf("unexpected couriers: %+v", resp.Couriers)
	}
	if _, err := newTestClient(srv).Serviceability(context.Background(), ServiceabilityRequest{}); err == nil {
		t.Fatal("expected error for missing postcodes")
	}
}

func TestMapStatus(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"PICKED UP", "picked_up", true},
		{"picked_up", "picked_up", true},
		{"Shipped", "in_transit", true},
		{"In Transit", "in_transit", true},
		{"IN_TRANSIT", "in_transit", true},
		{"Reached at Destination Hub", "in_transit", true},
		{"UNDELIVERED", "in_transit", true},
		{"Out For Delivery", "out_for_delivery", true},
		{"out-for-delivery", "out_for_delivery", true},
		{"Delivered", "delivered", true},
		{"RTO Delivered", "returned", true},
		{"RTO_ACKNOWLEDGED", "returned", true},
		{"LOST", "failed", true},
		{"RTO Initiated", "", false},
		{"RTO IN TRANSIT", "", false},
		{"Canceled", "", false},
		{"AWB Assigned", "", false},
		{"Pickup Scheduled", "", false},
		{"", "", false},
		{"something new", "", false},
	}
	for _, tc := range cases {
		got, ok := MapStatus(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MapStatus(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
