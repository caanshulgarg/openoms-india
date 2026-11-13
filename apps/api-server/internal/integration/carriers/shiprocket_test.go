package carriers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	shiprocket "github.com/openoms-org/openoms/packages/shiprocket-go-sdk"

	"github.com/openoms-org/openoms/apps/api-server/internal/integration"
)

// fakeShiprocket records calls and returns canned results.
type fakeShiprocket struct {
	calls []string

	createReq *shiprocket.CreateOrderRequest
	createErr error

	awbShipmentID, awbCourierID string
	awbErr                      error

	pickupErr error

	labelIDs []string
	labelURL string
	labelErr error

	downloadURL string
	downloadErr error

	tracking *shiprocket.TrackingData
	trackErr error

	cancelled []string
	cancelErr error

	svcReq  shiprocket.ServiceabilityRequest
	svcResp *shiprocket.ServiceabilityResponse
	svcErr  error
}

func (f *fakeShiprocket) CreateOrder(_ context.Context, req *shiprocket.CreateOrderRequest) (*shiprocket.CreateOrderResponse, error) {
	f.calls = append(f.calls, "create")
	f.createReq = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &shiprocket.CreateOrderResponse{OrderID: "16161616", ShipmentID: "15151515", Status: "NEW"}, nil
}

func (f *fakeShiprocket) AssignAWB(_ context.Context, shipmentID, courierID string) (*shiprocket.AWBAssignment, error) {
	f.calls = append(f.calls, "awb")
	f.awbShipmentID, f.awbCourierID = shipmentID, courierID
	if f.awbErr != nil {
		return nil, f.awbErr
	}
	return &shiprocket.AWBAssignment{AWBCode: "1091188857722", CourierCompanyID: "10", CourierName: "Delhivery Surface"}, nil
}

func (f *fakeShiprocket) GeneratePickup(_ context.Context, _ string) (*shiprocket.PickupResult, error) {
	f.calls = append(f.calls, "pickup")
	if f.pickupErr != nil {
		return nil, f.pickupErr
	}
	return &shiprocket.PickupResult{PickupStatus: 1}, nil
}

func (f *fakeShiprocket) GenerateLabel(_ context.Context, ids ...string) (string, error) {
	f.calls = append(f.calls, "label")
	f.labelIDs = ids
	return f.labelURL, f.labelErr
}

func (f *fakeShiprocket) Download(_ context.Context, fileURL string) ([]byte, error) {
	f.calls = append(f.calls, "download")
	f.downloadURL = fileURL
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	return []byte("%PDF-label"), nil
}

func (f *fakeShiprocket) TrackByAWB(_ context.Context, _ string) (*shiprocket.TrackingData, error) {
	return f.tracking, f.trackErr
}

func (f *fakeShiprocket) CancelOrders(_ context.Context, ids ...string) error {
	f.cancelled = append(f.cancelled, ids...)
	return f.cancelErr
}

func (f *fakeShiprocket) Serviceability(_ context.Context, req shiprocket.ServiceabilityRequest) (*shiprocket.ServiceabilityResponse, error) {
	f.svcReq = req
	return f.svcResp, f.svcErr
}

func newShiprocketTestProvider(f *fakeShiprocket, settings shiprocketSettings) *ShiprocketProvider {
	return &ShiprocketProvider{
		client:   f,
		settings: settings,
		logger:   slog.Default(),
		now:      func() time.Time { return time.Date(2026, 11, 13, 7, 54, 0, 0, time.UTC) }, // 13:24 IST
	}
}

func shiprocketSampleRequest() integration.CarrierShipmentRequest {
	return integration.CarrierShipmentRequest{
		OrderID:     "0b9c3e0e-0000-4000-8000-000000000001",
		Reference:   "0b9c3e0e-0000-4000-8000-000000000001",
		ServiceType: "standard",
		Receiver: integration.CarrierReceiver{
			Name: "Asha Rani Verma", Email: "asha@example.com", Phone: "+91 98765 43210",
			Street: "12 MG Road", City: "Bengaluru", PostalCode: "560001", Country: "IN",
		},
		Parcel:        integration.CarrierParcel{WeightKg: 0.5, WidthCm: 12, HeightCm: 5, DepthCm: 20},
		Items:         []integration.CarrierItem{{Name: "Cotton Shirt", SKU: "SH-1", Quantity: 2, UnitPrice: 499}},
		DeclaredValue: 998,
		Currency:      "INR",
	}
}

func TestShiprocketFactoryRegisteredAndValidates(t *testing.T) {
	p, err := integration.NewCarrierProvider("shiprocket",
		json.RawMessage(`{"email":" api@example.com ","password":"secret"}`),
		json.RawMessage(`{"pickup_location":" Noida WH ","default_courier_id":" 54 ","manual_pickup":true,"default_weight_kg":"0,75"}`))
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := p.(*ShiprocketProvider)
	if !ok {
		t.Fatalf("unexpected provider type %T", p)
	}
	if sp.ProviderName() != "shiprocket" || sp.settings.PickupLocation != "Noida WH" || sp.settings.DefaultCourierID != "54" {
		t.Fatalf("unexpected provider: %+v", sp.settings)
	}
	if !sp.settings.ManualPickup {
		t.Fatal("manual_pickup:true must be preserved")
	}
	if sp.settings.DefaultWeightKg != 0.75 {
		t.Fatalf("string weight from the dashboard form must be accepted, got %v", sp.settings.DefaultWeightKg)
	}
	if sp.SupportsPickupPoints() {
		t.Error("shiprocket must not advertise pickup points")
	}
	if _, err := sp.SearchPickupPoints(context.Background(), "x"); err == nil {
		t.Error("SearchPickupPoints must fail")
	}

	bad := []struct{ creds, settings string }{
		{`{"email":"","password":"x"}`, `{}`},
		{`{"email":"a@b.c","password":""}`, `{}`},
		{`not json`, `{}`},
		{`{"email":"a@b.c","password":"x"}`, `not json`},
		{`{"email":"a@b.c","password":"x"}`, `{"default_weight_kg":"heavy"}`},
		{`{"email":"a@b.c","password":"x"}`, `{"default_weight_kg":-1}`},
	}
	for _, tc := range bad {
		if _, err := NewShiprocketProvider(json.RawMessage(tc.creds), json.RawMessage(tc.settings)); err == nil {
			t.Errorf("expected error for creds=%s settings=%s", tc.creds, tc.settings)
		}
	}
	// Settings are optional at construction time (needed for rates/tracking).
	if _, err := NewShiprocketProvider(json.RawMessage(`{"email":"a@b.c","password":"x"}`), nil); err != nil {
		t.Errorf("nil settings must be accepted: %v", err)
	}
}

func TestShiprocketCreateShipmentPrepaid(t *testing.T) {
	f := &fakeShiprocket{}
	p := newShiprocketTestProvider(f, shiprocketSettings{PickupLocation: "Noida WH"})

	resp, err := p.CreateShipment(context.Background(), shiprocketSampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExternalID != "16161616:15151515" || resp.TrackingNumber != "1091188857722" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if got := strings.Join(f.calls, ","); got != "create,awb,pickup" {
		t.Fatalf("call order = %s", got)
	}
	if f.awbShipmentID != "15151515" || f.awbCourierID != "" {
		t.Errorf("awb args = %q, %q (courier must be empty for auto)", f.awbShipmentID, f.awbCourierID)
	}

	r := f.createReq
	if r.OrderID != "0b9c3e0e-0000-4000-8000-000000000001" || r.PickupLocation != "Noida WH" {
		t.Errorf("order id / pickup = %q / %q", r.OrderID, r.PickupLocation)
	}
	if r.OrderDate != "2026-11-13 13:24" {
		t.Errorf("order date must be IST, got %q", r.OrderDate)
	}
	if r.BillingCustomerName != "Asha Rani" || r.BillingLastName != "Verma" {
		t.Errorf("name split = %q / %q", r.BillingCustomerName, r.BillingLastName)
	}
	if r.BillingPhone != "9876543210" || r.BillingPincode != "560001" || r.BillingState != "Karnataka" || r.BillingCountry != "India" {
		t.Errorf("address fields = %+v", r)
	}
	if !r.ShippingIsBilling || r.PaymentMethod != "Prepaid" || r.SubTotal != 998 {
		t.Errorf("payment = %v / %q / %v", r.ShippingIsBilling, r.PaymentMethod, r.SubTotal)
	}
	if r.Weight != 0.5 || r.Length != 20 || r.Breadth != 12 || r.Height != 5 {
		t.Errorf("parcel = %v kg %vx%vx%v", r.Weight, r.Length, r.Breadth, r.Height)
	}
	if len(r.OrderItems) != 1 || r.OrderItems[0].SKU != "SH-1" || r.OrderItems[0].Units != 2 || r.OrderItems[0].SellingPrice != 499 {
		t.Errorf("items = %+v", r.OrderItems)
	}
}

func TestShiprocketCreateShipmentCODAndCourierSelection(t *testing.T) {
	f := &fakeShiprocket{}
	p := newShiprocketTestProvider(f, shiprocketSettings{PickupLocation: "Noida WH", DefaultCourierID: "54", ManualPickup: true})

	req := shiprocketSampleRequest()
	req.CODAmount = 1050 // includes COD fee: differs from the order total
	req.ServiceType = ""
	if _, err := p.CreateShipment(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if f.createReq.PaymentMethod != "COD" || f.createReq.SubTotal != 1050 {
		t.Errorf("COD: payment=%q sub_total=%v, want COD / 1050", f.createReq.PaymentMethod, f.createReq.SubTotal)
	}
	if f.awbCourierID != "54" {
		t.Errorf("default courier not used: %q", f.awbCourierID)
	}
	if got := strings.Join(f.calls, ","); got != "create,awb" {
		t.Errorf("manual_pickup must skip the pickup request, calls = %s", got)
	}

	// An explicit numeric service type (courier id from rate shopping) wins.
	f.calls = nil
	req.ServiceType = "10"
	if _, err := p.CreateShipment(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if f.awbCourierID != "10" {
		t.Errorf("explicit courier not used: %q", f.awbCourierID)
	}
}

func TestShiprocketCreateShipmentDefaultsAndFallbackItem(t *testing.T) {
	f := &fakeShiprocket{}
	p := newShiprocketTestProvider(f, shiprocketSettings{
		PickupLocation: "Noida WH", DefaultWeightKg: 0.75, DefaultLengthCm: 30, DefaultWidthCm: 20,
	})
	req := shiprocketSampleRequest()
	req.Parcel = integration.CarrierParcel{}
	req.Items = nil
	req.Receiver.State = "KA (explicit)"
	if _, err := p.CreateShipment(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	r := f.createReq
	if r.Weight != 0.75 || r.Length != 30 || r.Breadth != 20 || r.Height != 10 {
		t.Errorf("defaults not applied: %v kg %vx%vx%v", r.Weight, r.Length, r.Breadth, r.Height)
	}
	if r.BillingState != "KA (explicit)" {
		t.Errorf("explicit state must be used, got %q", r.BillingState)
	}
	if len(r.OrderItems) != 1 || r.OrderItems[0].Units != 1 || r.OrderItems[0].SellingPrice != 998 || r.OrderItems[0].SKU == "" {
		t.Errorf("fallback item wrong: %+v", r.OrderItems)
	}
}

func TestShiprocketCreateShipmentRejectsBadInputBeforeCallingAPI(t *testing.T) {
	cases := map[string]struct {
		settings shiprocketSettings
		mutate   func(*integration.CarrierShipmentRequest)
		want     string
	}{
		"no pickup location": {shiprocketSettings{}, func(*integration.CarrierShipmentRequest) {}, "pickup location is not configured"},
		"bad service type":   {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.ServiceType = "inpost_locker_standard" }, "unknown service type"},
		"non-INR order":      {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Currency = "PLN" }, "must be in INR"},
		"non-INR COD":        {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.CODCurrency = "PLN" }, "COD must be in INR"},
		"polish postcode":    {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Receiver.PostalCode = "00-950" }, "6-digit PIN"},
		"bad phone":          {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Receiver.Phone = "123" }, "10-digit"},
		"APS pin, no state":  {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Receiver.PostalCode = "900001" }, "state is required"},
		"no weight":          {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Parcel.WeightKg = 0 }, "weight is required"},
		"no reference": {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) {
			r.Reference, r.OrderID = "", ""
		}, "order reference is required"},
		"no value": {shiprocketSettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) {
			r.DeclaredValue, r.Items = 0, nil
		}, "order value is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeShiprocket{}
			p := newShiprocketTestProvider(f, tc.settings)
			req := shiprocketSampleRequest()
			tc.mutate(&req)
			_, err := p.CreateShipment(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
			if len(f.calls) != 0 {
				t.Fatalf("no API call may be made on invalid input, got %v", f.calls)
			}
		})
	}
}

func TestShiprocketCreateShipmentFailures(t *testing.T) {
	f := &fakeShiprocket{createErr: errors.New("boom")}
	p := newShiprocketTestProvider(f, shiprocketSettings{PickupLocation: "x"})
	if _, err := p.CreateShipment(context.Background(), shiprocketSampleRequest()); err == nil || !strings.Contains(err.Error(), "create order") {
		t.Fatalf("unexpected error: %v", err)
	}

	// AWB failure: error names the orphaned Shiprocket order, no pickup attempted.
	f = &fakeShiprocket{awbErr: errors.New("Recharge your wallet")}
	p = newShiprocketTestProvider(f, shiprocketSettings{PickupLocation: "x"})
	_, err := p.CreateShipment(context.Background(), shiprocketSampleRequest())
	if err == nil || !strings.Contains(err.Error(), "16161616") || !strings.Contains(err.Error(), "Recharge your wallet") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(f.calls, ","); got != "create,awb" {
		t.Fatalf("calls = %s", got)
	}

	// Pickup failure is not fatal: the AWB is valid.
	f = &fakeShiprocket{pickupErr: errors.New("pickup slot unavailable")}
	p = newShiprocketTestProvider(f, shiprocketSettings{PickupLocation: "x"})
	resp, err := p.CreateShipment(context.Background(), shiprocketSampleRequest())
	if err != nil || resp.TrackingNumber == "" {
		t.Fatalf("pickup failure must not fail the shipment: resp=%+v err=%v", resp, err)
	}
}

func TestShiprocketGetLabel(t *testing.T) {
	f := &fakeShiprocket{labelURL: "https://files.example.com/l.pdf"}
	p := newShiprocketTestProvider(f, shiprocketSettings{})
	data, err := p.GetLabel(context.Background(), "16161616:15151515", "pdf")
	if err != nil || string(data) != "%PDF-label" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if len(f.labelIDs) != 1 || f.labelIDs[0] != "15151515" || f.downloadURL != "https://files.example.com/l.pdf" {
		t.Errorf("label ids=%v download=%q", f.labelIDs, f.downloadURL)
	}
	// A bare id is treated as the shipment id.
	if _, err := p.GetLabel(context.Background(), "777", ""); err != nil || f.labelIDs[0] != "777" {
		t.Errorf("bare id: ids=%v err=%v", f.labelIDs, err)
	}
	if _, err := p.GetLabel(context.Background(), "1:2", "zpl"); err == nil || !strings.Contains(err.Error(), "PDF only") {
		t.Errorf("zpl must be rejected: %v", err)
	}
	if _, err := p.GetLabel(context.Background(), "", "pdf"); err == nil {
		t.Error("empty external id must fail")
	}
	f.labelErr = errors.New("AWB not assigned")
	if _, err := p.GetLabel(context.Background(), "1:2", "pdf"); err == nil {
		t.Error("label error must propagate")
	}
	f.labelErr, f.downloadErr = nil, errors.New("403")
	if _, err := p.GetLabel(context.Background(), "1:2", "pdf"); err == nil {
		t.Error("download error must propagate")
	}
}

func TestShiprocketGetTrackingIsChronologicalAndMappable(t *testing.T) {
	f := &fakeShiprocket{tracking: &shiprocket.TrackingData{
		TrackStatus: 1,
		Activities: []shiprocket.TrackActivity{ // newest first, as the API returns them
			{Date: "2026-11-16 14:02:00", Activity: "Delivered to consignee", Location: "Bengaluru", SRStatusLabel: "DELIVERED"},
			{Date: "2026-11-16 09:00:00", Activity: "Out for delivery", Location: "Bengaluru", SRStatusLabel: "OUT FOR DELIVERY"},
			{Date: "2026-11-15 02:00:00", Activity: "Bag received at facility", Location: "Hosur", SRStatusLabel: "NA"},
			{Date: "2026-11-14 09:10:00", Activity: "Shipment picked up", Location: "Noida", SRStatusLabel: "PICKED UP"},
			{Date: "", Activity: "", SRStatusLabel: ""},
		},
	}}
	p := newShiprocketTestProvider(f, shiprocketSettings{})
	events, err := p.GetTracking(context.Background(), "1091188857722")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("len(events) = %d, want 4 (blank activity dropped)", len(events))
	}
	if events[0].Status != "PICKED UP" || events[3].Status != "DELIVERED" {
		t.Fatalf("events not chronological: first=%q last=%q", events[0].Status, events[3].Status)
	}
	if events[1].Status != "Bag received at facility" {
		t.Errorf("NA label must fall back to the activity text, got %q", events[1].Status)
	}
	wantTS := time.Date(2026, 11, 16, 8, 32, 0, 0, time.UTC) // 14:02 IST
	if !events[3].Timestamp.Equal(wantTS) {
		t.Errorf("timestamp = %v, want %v", events[3].Timestamp.UTC(), wantTS)
	}
	if st, ok := p.MapStatus(events[3].Status); !ok || st != "delivered" {
		t.Errorf("last event must map to delivered, got %q %v", st, ok)
	}
	if _, ok := p.MapStatus(events[1].Status); ok {
		t.Error("free-text activity must not map to a status")
	}
}

func TestShiprocketGetTrackingFallsBackToCurrentStatus(t *testing.T) {
	f := &fakeShiprocket{tracking: &shiprocket.TrackingData{
		ShipmentTrack: []shiprocket.ShipmentTrack{{CurrentStatus: "Pickup Scheduled", Destination: "Bengaluru"}},
	}}
	p := newShiprocketTestProvider(f, shiprocketSettings{})
	events, err := p.GetTracking(context.Background(), "x")
	if err != nil || len(events) != 1 || events[0].Status != "Pickup Scheduled" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if _, ok := p.MapStatus(events[0].Status); ok {
		t.Error("pre-pickup status must not change the OMS shipment")
	}

	f.tracking = &shiprocket.TrackingData{}
	if events, err := p.GetTracking(context.Background(), "x"); err != nil || len(events) != 0 {
		t.Fatalf("no data must yield no events: %+v %v", events, err)
	}
	f.trackErr = errors.New("404")
	if _, err := p.GetTracking(context.Background(), "x"); err == nil {
		t.Fatal("tracking error must propagate")
	}
}

func TestShiprocketCancel(t *testing.T) {
	f := &fakeShiprocket{}
	p := newShiprocketTestProvider(f, shiprocketSettings{})
	if err := p.CancelShipment(context.Background(), "16161616:15151515"); err != nil {
		t.Fatal(err)
	}
	if len(f.cancelled) != 1 || f.cancelled[0] != "16161616" {
		t.Fatalf("must cancel by ORDER id, got %v", f.cancelled)
	}
	if err := p.CancelShipment(context.Background(), "15151515"); err == nil {
		t.Fatal("an external id without an order id must not be cancelled blindly")
	}
	f.cancelErr = errors.New("already shipped")
	if err := p.CancelShipment(context.Background(), "1:2"); err == nil {
		t.Fatal("cancel error must propagate")
	}
}

func TestShiprocketGetRates(t *testing.T) {
	f := &fakeShiprocket{svcResp: &shiprocket.ServiceabilityResponse{Couriers: []shiprocket.CourierOption{
		{CourierCompanyID: "10", CourierName: "Delhivery Surface", Rate: 84.5, EstimatedDeliveryDays: "4"},
		{CourierCompanyID: "54", CourierName: "Bluedart Air", Rate: 132, EstimatedDeliveryDays: " 2 "},
		{CourierCompanyID: "", CourierName: "Broken", Rate: 10},
		{CourierCompanyID: "99", CourierName: "Zero", Rate: 0},
	}}}
	p := newShiprocketTestProvider(f, shiprocketSettings{})
	rates, err := p.GetRates(context.Background(), integration.RateRequest{
		FromPostalCode: "201 301", ToPostalCode: "560001", Weight: 0.5, Width: 12, Height: 5, Length: 20, COD: 998,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("len(rates) = %d, want 2 (invalid couriers dropped)", len(rates))
	}
	r := rates[0]
	if r.CarrierCode != "shiprocket" || r.ServiceName != "10" || r.Price != 84.5 || r.Currency != "INR" || r.EstimatedDays != 4 || r.IsEstimate {
		t.Errorf("unexpected rate: %+v", r)
	}
	if rates[1].EstimatedDays != 2 {
		t.Errorf("estimated days = %d", rates[1].EstimatedDays)
	}
	if f.svcReq.PickupPostcode != "201301" || f.svcReq.DeliveryPostcode != "560001" || !f.svcReq.COD || f.svcReq.DeclaredValue != 998 ||
		f.svcReq.LengthCm != 20 || f.svcReq.BreadthCm != 12 || f.svcReq.HeightCm != 5 {
		t.Errorf("unexpected serviceability request: %+v", f.svcReq)
	}
	// A rate's ServiceName must round-trip as a valid service type.
	if id, err := p.resolveShiprocketCourier(r.ServiceName); err != nil || id != "10" {
		t.Errorf("rate service name does not round-trip: %q %v", id, err)
	}

	// Non-Indian lane: nothing to quote, and no API call.
	f.svcReq = shiprocket.ServiceabilityRequest{}
	rates, err = p.GetRates(context.Background(), integration.RateRequest{FromPostalCode: "00-950", ToPostalCode: "30-001", Weight: 1})
	if err != nil || rates != nil || f.svcReq.PickupPostcode != "" {
		t.Errorf("polish lane: rates=%v err=%v req=%+v", rates, err, f.svcReq)
	}

	f.svcErr = errors.New("500")
	if _, err := p.GetRates(context.Background(), integration.RateRequest{FromPostalCode: "201301", ToPostalCode: "560001"}); err == nil {
		t.Error("serviceability error must propagate")
	}
	if f.svcReq.WeightKg != 0.5 {
		t.Errorf("missing weight must default to 0.5 kg, got %v", f.svcReq.WeightKg)
	}
}

func TestShiprocketHelpers(t *testing.T) {
	names := map[string][2]string{
		"Asha":               {"Asha", ""},
		"Asha Verma":         {"Asha", "Verma"},
		"  Asha  Rani Verma": {"Asha Rani", "Verma"},
		"":                   {"", ""},
	}
	for in, want := range names {
		first, last := splitPersonName(in)
		if first != want[0] || last != want[1] {
			t.Errorf("splitPersonName(%q) = (%q, %q), want %v", in, first, last, want)
		}
	}
	if o, s := parseShiprocketExternalID(" 1:2 "); o != "1" || s != "2" {
		t.Errorf("parse = %q, %q", o, s)
	}
	if got := shiprocketExternalID("1", "2"); got != "1:2" {
		t.Errorf("external id = %q", got)
	}
	if firstPositive(0, -1, 3, 4) != 3 || firstPositive() != 0 {
		t.Error("firstPositive wrong")
	}
	if !parseShiprocketTime("garbage").IsZero() {
		t.Error("garbage time must be zero")
	}
}
