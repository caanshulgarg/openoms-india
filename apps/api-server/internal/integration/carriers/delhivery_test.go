package carriers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	delhivery "github.com/openoms-org/openoms/packages/delhivery-go-sdk"

	"github.com/openoms-org/openoms/apps/api-server/internal/integration"
)

// fakeDelhivery records calls and returns canned results.
type fakeDelhivery struct {
	calls int

	createReq  *delhivery.CreateRequest
	createResp *delhivery.CreateResponse
	createErr  error

	slipWaybill, slipSize string
	slipErr               error
	downloadURL           string
	downloadErr           error

	tracked  []delhivery.TrackedShipment
	trackErr error

	cancelled string
	cancelErr error

	chargeReqs []delhivery.ChargesRequest
	charges    map[string][]delhivery.Charge
	chargeErrs map[string]error
}

func (f *fakeDelhivery) CreateShipment(_ context.Context, req *delhivery.CreateRequest) (*delhivery.CreateResponse, error) {
	f.calls++
	f.createReq = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &delhivery.CreateResponse{Success: true, Packages: []delhivery.CreatedPackage{{Status: "Success", Waybill: "1490811234567"}}}, nil
}

func (f *fakeDelhivery) PackingSlip(_ context.Context, waybill, pdfSize string) (*delhivery.PackingSlip, error) {
	f.slipWaybill, f.slipSize = waybill, pdfSize
	if f.slipErr != nil {
		return nil, f.slipErr
	}
	return &delhivery.PackingSlip{PDFDownloadLink: "https://files.example.com/slip.pdf", PackagesFound: 1}, nil
}

func (f *fakeDelhivery) Download(_ context.Context, fileURL string) ([]byte, error) {
	f.downloadURL = fileURL
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	return []byte("%PDF-slip"), nil
}

func (f *fakeDelhivery) Track(_ context.Context, _ ...string) ([]delhivery.TrackedShipment, error) {
	return f.tracked, f.trackErr
}

func (f *fakeDelhivery) Cancel(_ context.Context, waybill string) error {
	f.cancelled = waybill
	return f.cancelErr
}

func (f *fakeDelhivery) ShippingCharges(_ context.Context, req delhivery.ChargesRequest) ([]delhivery.Charge, error) {
	f.chargeReqs = append(f.chargeReqs, req)
	if err := f.chargeErrs[req.Mode]; err != nil {
		return nil, err
	}
	return f.charges[req.Mode], nil
}

func newDelhiveryTestProvider(f *fakeDelhivery, settings delhiverySettings) *DelhiveryProvider {
	if settings.ShippingMode == "" {
		settings.ShippingMode = delhivery.ModeSurface
	}
	return &DelhiveryProvider{
		client:   f,
		settings: settings,
		logger:   slog.Default(),
		now:      func() time.Time { return time.Date(2026, 11, 13, 7, 54, 0, 0, time.UTC) }, // 13:24 IST
	}
}

func delhiverySampleRequest() integration.CarrierShipmentRequest {
	return integration.CarrierShipmentRequest{
		OrderID:     "0b9c3e0e-0000-4000-8000-000000000001",
		Reference:   "0b9c3e0e-0000-4000-8000-000000000001",
		ServiceType: "standard",
		Receiver: integration.CarrierReceiver{
			Name: "Asha Verma", Phone: "09876543210", Street: "12 MG Road",
			City: "Bengaluru", PostalCode: "560001", Country: "India",
		},
		Parcel: integration.CarrierParcel{WeightKg: 0.75, WidthCm: 12, HeightCm: 5, DepthCm: 20},
		Items: []integration.CarrierItem{
			{Name: "Cotton Shirt", SKU: "SH-1", Quantity: 2, UnitPrice: 499},
			{Name: "", SKU: "CAP-9", Quantity: 1, UnitPrice: 199},
		},
		DeclaredValue: 1197,
		Currency:      "INR",
	}
}

func TestDelhiveryFactoryRegisteredAndValidates(t *testing.T) {
	p, err := integration.NewCarrierProvider("delhivery",
		json.RawMessage(`{"api_token":" tok ","sandbox":true}`),
		json.RawMessage(`{"pickup_location":" Noida WH ","shipping_mode":"express","label_size":"4r","default_weight_kg":"0.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	dp, ok := p.(*DelhiveryProvider)
	if !ok {
		t.Fatalf("unexpected provider type %T", p)
	}
	if dp.ProviderName() != "delhivery" || dp.settings.PickupLocation != "Noida WH" ||
		dp.settings.ShippingMode != "Express" || dp.settings.LabelSize != "4R" || dp.settings.DefaultWeightKg != 0.4 {
		t.Fatalf("unexpected settings: %+v", dp.settings)
	}
	if dp.SupportsPickupPoints() {
		t.Error("delhivery must not advertise pickup points")
	}
	if _, err := dp.SearchPickupPoints(context.Background(), "x"); err == nil {
		t.Error("SearchPickupPoints must fail")
	}

	// Defaults.
	dflt, err := NewDelhiveryProvider(json.RawMessage(`{"api_token":"tok"}`), nil)
	if err != nil || dflt.settings.ShippingMode != "Surface" || dflt.settings.LabelSize != "" {
		t.Fatalf("defaults wrong: %+v err=%v", dflt, err)
	}

	bad := []struct{ creds, settings string }{
		{`{"api_token":"  "}`, `{}`},
		{`not json`, `{}`},
		{`{"api_token":"t"}`, `not json`},
		{`{"api_token":"t"}`, `{"shipping_mode":"rocket"}`},
		{`{"api_token":"t"}`, `{"label_size":"A5"}`},
	}
	for _, tc := range bad {
		if _, err := NewDelhiveryProvider(json.RawMessage(tc.creds), json.RawMessage(tc.settings)); err == nil {
			t.Errorf("expected error for creds=%s settings=%s", tc.creds, tc.settings)
		}
	}
}

func TestDelhiveryCreateShipmentPrepaid(t *testing.T) {
	f := &fakeDelhivery{}
	p := newDelhiveryTestProvider(f, delhiverySettings{
		PickupLocation: "Noida WH", SellerName: "Acme Retail", SellerGSTIN: "09ABCDE1234F1Z5", DefaultHSNCode: "6205",
	})
	resp, err := p.CreateShipment(context.Background(), delhiverySampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExternalID != "1490811234567" || resp.TrackingNumber != "1490811234567" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if _, ok := p.MapStatus(resp.Status); ok {
		t.Error("the initial Manifested status must not move the OMS shipment")
	}

	if f.createReq.PickupLocation.Name != "Noida WH" || len(f.createReq.Shipments) != 1 {
		t.Fatalf("unexpected request: %+v", f.createReq)
	}
	s := f.createReq.Shipments[0]
	if s.Name != "Asha Verma" || s.Phone != "9876543210" || s.Pin != "560001" || s.State != "Karnataka" || s.Country != "India" {
		t.Errorf("consignee = %+v", s)
	}
	if s.PaymentMode != "Prepaid" || s.CODAmount != 0 || s.TotalAmount != 1197 {
		t.Errorf("payment = %q cod=%v total=%v", s.PaymentMode, s.CODAmount, s.TotalAmount)
	}
	if s.Weight != 750 {
		t.Errorf("weight must be sent in grams: got %v, want 750", s.Weight)
	}
	if s.Length != 20 || s.Width != 12 || s.Height != 5 {
		t.Errorf("dimensions = %vx%vx%v", s.Length, s.Width, s.Height)
	}
	if s.ShippingMode != "Surface" || s.Waybill != "" {
		t.Errorf("mode=%q waybill=%q (waybill must be empty for auto-assignment)", s.ShippingMode, s.Waybill)
	}
	if s.ProductsDesc != "Cotton Shirt x2, CAP-9 x1" || s.Quantity != 3 {
		t.Errorf("products = %q qty=%d", s.ProductsDesc, s.Quantity)
	}
	if s.SellerName != "Acme Retail" || s.SellerGSTTIN != "09ABCDE1234F1Z5" || s.HSNCode != "6205" {
		t.Errorf("seller fields = %+v", s)
	}
	if s.OrderDate != "2026-11-13 13:24:00" {
		t.Errorf("order date must be IST, got %q", s.OrderDate)
	}
	if s.Order != "0b9c3e0e-0000-4000-8000-000000000001" {
		t.Errorf("order ref = %q", s.Order)
	}
}

func TestDelhiveryCreateShipmentCODAndModes(t *testing.T) {
	f := &fakeDelhivery{}
	p := newDelhiveryTestProvider(f, delhiverySettings{PickupLocation: "Noida WH", DefaultWeightKg: 0.4})

	req := delhiverySampleRequest()
	req.CODAmount = 1250 // above the order total (COD fee): total must follow
	req.ServiceType = "express"
	req.Parcel = integration.CarrierParcel{}
	req.Items = nil
	if _, err := p.CreateShipment(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	s := f.createReq.Shipments[0]
	if s.PaymentMode != "COD" || s.CODAmount != 1250 || s.TotalAmount != 1250 {
		t.Errorf("COD: mode=%q cod=%v total=%v", s.PaymentMode, s.CODAmount, s.TotalAmount)
	}
	if s.ShippingMode != "Express" {
		t.Errorf("mode = %q", s.ShippingMode)
	}
	if s.Weight != 400 {
		t.Errorf("default weight not applied: %v", s.Weight)
	}
	if !strings.HasPrefix(s.ProductsDesc, "Order ") || s.Quantity != 1 {
		t.Errorf("fallback description wrong: %q qty=%d", s.ProductsDesc, s.Quantity)
	}

	for st, want := range map[string]string{"": "Surface", "standard": "Surface", "SURFACE": "Surface", "air": "Express"} {
		got, err := p.resolveDelhiveryMode(st)
		if err != nil || got != want {
			t.Errorf("resolveDelhiveryMode(%q) = %q, %v; want %q", st, got, err, want)
		}
	}
}

func TestDelhiveryCreateShipmentRejectsBadInputBeforeCallingAPI(t *testing.T) {
	cases := map[string]struct {
		settings delhiverySettings
		mutate   func(*integration.CarrierShipmentRequest)
		want     string
	}{
		"no pickup location": {delhiverySettings{}, func(*integration.CarrierShipmentRequest) {}, "pickup location is not configured"},
		"bad service type":   {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.ServiceType = "express_10" }, "unknown service type"},
		"non-INR order":      {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Currency = "PLN" }, "must be in INR"},
		"non-INR COD":        {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.CODCurrency = "EUR" }, "COD must be in INR"},
		"foreign country":    {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Receiver.Country = "PL" }, "within India"},
		"bad pin":            {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Receiver.PostalCode = "5600" }, "6-digit PIN"},
		"bad phone":          {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Receiver.Phone = "" }, "10-digit"},
		"no weight":          {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) { r.Parcel.WeightKg = 0 }, "weight is required"},
		"no reference": {delhiverySettings{PickupLocation: "x"}, func(r *integration.CarrierShipmentRequest) {
			r.Reference, r.OrderID = "", ""
		}, "order reference is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeDelhivery{}
			p := newDelhiveryTestProvider(f, tc.settings)
			req := delhiverySampleRequest()
			tc.mutate(&req)
			_, err := p.CreateShipment(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
			if f.calls != 0 {
				t.Fatal("no API call may be made on invalid input")
			}
		})
	}
}

func TestDelhiveryCreateShipmentFailures(t *testing.T) {
	f := &fakeDelhivery{createErr: errors.New("ClientWarehouse matching query does not exist")}
	p := newDelhiveryTestProvider(f, delhiverySettings{PickupLocation: "x"})
	if _, err := p.CreateShipment(context.Background(), delhiverySampleRequest()); err == nil || !strings.Contains(err.Error(), "ClientWarehouse") {
		t.Fatalf("unexpected error: %v", err)
	}
	f = &fakeDelhivery{createResp: &delhivery.CreateResponse{Success: true}}
	p = newDelhiveryTestProvider(f, delhiverySettings{PickupLocation: "x"})
	if _, err := p.CreateShipment(context.Background(), delhiverySampleRequest()); err == nil || !strings.Contains(err.Error(), "no waybill") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDelhiveryGetLabel(t *testing.T) {
	f := &fakeDelhivery{}
	p := newDelhiveryTestProvider(f, delhiverySettings{LabelSize: "4R"})
	data, err := p.GetLabel(context.Background(), "1490811234567", "pdf")
	if err != nil || string(data) != "%PDF-slip" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if f.slipWaybill != "1490811234567" || f.slipSize != "4R" || f.downloadURL != "https://files.example.com/slip.pdf" {
		t.Errorf("slip=%q size=%q url=%q", f.slipWaybill, f.slipSize, f.downloadURL)
	}
	if _, err := p.GetLabel(context.Background(), "1", "zpl"); err == nil || !strings.Contains(err.Error(), "PDF only") {
		t.Errorf("zpl must be rejected: %v", err)
	}
	f.slipErr = errors.New("no label found")
	if _, err := p.GetLabel(context.Background(), "1", ""); err == nil {
		t.Error("slip error must propagate")
	}
	f.slipErr, f.downloadErr = nil, errors.New("403")
	if _, err := p.GetLabel(context.Background(), "1", ""); err == nil {
		t.Error("download error must propagate")
	}
}

func scan(scanType, status, when, where, note string) struct {
	ScanDetail delhivery.ScanDetail `json:"ScanDetail"`
} {
	return struct {
		ScanDetail delhivery.ScanDetail `json:"ScanDetail"`
	}{ScanDetail: delhivery.ScanDetail{ScanType: scanType, Scan: status, ScanDateTime: when, ScannedLocation: where, Instructions: note}}
}

func TestDelhiveryGetTracking(t *testing.T) {
	ship := delhivery.TrackedShipment{AWB: "111"}
	ship.Scans = append(ship.Scans,
		scan("UD", "Manifested", "2026-11-13T19:20:05.002", "Noida_WH (Uttar Pradesh)", "Consignment Manifested"),
		scan("UD", "In Transit", "2026-11-14T09:10:00", "Noida_Sector63_PC (Uttar Pradesh)", "Shipment picked up"),
		scan("UD", "", "2026-11-14T10:00:00", "", "blank scans are skipped"),
		scan("UD", "Dispatched", "2026-11-16T09:00:00", "Bengaluru_Kormngla_D (Karnataka)", "Out for delivery"),
	)
	// The summary is ahead of the scan list: delivered.
	ship.Status = delhivery.TrackStatus{Status: "Delivered", StatusType: "DL", StatusDateTime: "2026-11-16T14:02:11.767",
		StatusLocation: "Bengaluru_Kormngla_D (Karnataka)", Instructions: "Delivered to consignee"}

	f := &fakeDelhivery{tracked: []delhivery.TrackedShipment{ship}}
	p := newDelhiveryTestProvider(f, delhiverySettings{})
	events, err := p.GetTracking(context.Background(), "111")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("len(events) = %d, want 4 (3 scans + summary)", len(events))
	}
	wantStatuses := []string{"UD:Manifested", "UD:In Transit", "UD:Dispatched", "DL:Delivered"}
	wantOMS := []string{"", "in_transit", "out_for_delivery", "delivered"}
	for i, ev := range events {
		if ev.Status != wantStatuses[i] {
			t.Errorf("events[%d].Status = %q, want %q", i, ev.Status, wantStatuses[i])
		}
		got, _ := p.MapStatus(ev.Status)
		if got != wantOMS[i] {
			t.Errorf("MapStatus(%q) = %q, want %q", ev.Status, got, wantOMS[i])
		}
	}
	wantTS := time.Date(2026, 11, 16, 8, 32, 11, 767000000, time.UTC) // 14:02:11.767 IST
	if !events[3].Timestamp.Equal(wantTS) || events[3].Details != "Delivered to consignee" {
		t.Errorf("summary event = %+v", events[3])
	}

	// Summary equal to the last scan must not be duplicated.
	ship.Status = delhivery.TrackStatus{Status: "Dispatched", StatusType: "UD"}
	f.tracked = []delhivery.TrackedShipment{ship}
	if events, _ := p.GetTracking(context.Background(), "111"); len(events) != 3 {
		t.Errorf("duplicate summary appended: %d events", len(events))
	}

	// RTO: returning parcels stay unmapped until they are back (DL:RTO).
	rto := delhivery.TrackedShipment{Status: delhivery.TrackStatus{Status: "In Transit", StatusType: "RT"}}
	f.tracked = []delhivery.TrackedShipment{rto}
	events, _ = p.GetTracking(context.Background(), "111")
	if len(events) != 1 || events[0].Status != "RT:In Transit" || events[0].Timestamp.IsZero() {
		t.Fatalf("rto events = %+v", events)
	}
	if _, ok := p.MapStatus(events[0].Status); ok {
		t.Error("RT in transit must not map")
	}
	if st, ok := p.MapStatus("DL:RTO"); !ok || st != "returned" {
		t.Errorf("DL:RTO = %q %v", st, ok)
	}

	f.tracked = nil
	if events, err := p.GetTracking(context.Background(), "111"); err != nil || events != nil {
		t.Errorf("no data must yield no events: %v %v", events, err)
	}
	f.trackErr = errors.New("401")
	if _, err := p.GetTracking(context.Background(), "111"); err == nil {
		t.Error("tracking error must propagate")
	}
}

func TestDelhiveryCancel(t *testing.T) {
	f := &fakeDelhivery{}
	p := newDelhiveryTestProvider(f, delhiverySettings{})
	if err := p.CancelShipment(context.Background(), "111"); err != nil || f.cancelled != "111" {
		t.Fatalf("cancelled=%q err=%v", f.cancelled, err)
	}
	f.cancelErr = errors.New("already delivered")
	if err := p.CancelShipment(context.Background(), "111"); err == nil {
		t.Fatal("cancel error must propagate")
	}
}

func TestDelhiveryGetRates(t *testing.T) {
	f := &fakeDelhivery{charges: map[string][]delhivery.Charge{
		"Surface": {{TotalAmount: 96.5}},
		"Express": {{TotalAmount: 160.83}},
	}}
	p := newDelhiveryTestProvider(f, delhiverySettings{})
	rates, err := p.GetRates(context.Background(), integration.RateRequest{
		FromPostalCode: "201301", ToPostalCode: "560 001", Weight: 0.5, Length: 40, Width: 30, Height: 25, COD: 998,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("len(rates) = %d", len(rates))
	}
	if rates[0].ServiceName != "surface" || rates[0].Price != 96.5 || rates[0].Currency != "INR" || !rates[0].IsEstimate || rates[0].CarrierCode != "delhivery" {
		t.Errorf("surface rate = %+v", rates[0])
	}
	if rates[1].ServiceName != "express" || rates[1].Price != 160.83 {
		t.Errorf("express rate = %+v", rates[1])
	}
	// 40x30x25 / 5000 = 6 kg volumetric > 0.5 kg dead weight.
	for _, cr := range f.chargeReqs {
		if cr.WeightGrams != 6000 {
			t.Errorf("chargeable weight = %d g, want 6000 (volumetric)", cr.WeightGrams)
		}
		if cr.OriginPin != "201301" || cr.DestinationPin != "560001" || !cr.COD || cr.CODAmount != 998 {
			t.Errorf("unexpected charges request: %+v", cr)
		}
	}
	// Rate service names must round-trip as service types.
	for _, r := range rates {
		if _, err := p.resolveDelhiveryMode(r.ServiceName); err != nil {
			t.Errorf("service %q does not round-trip: %v", r.ServiceName, err)
		}
	}

	// One mode failing still returns the other.
	f.chargeErrs = map[string]error{"Express": errors.New("timeout")}
	f.chargeReqs = nil
	rates, err = p.GetRates(context.Background(), integration.RateRequest{FromPostalCode: "201301", ToPostalCode: "560001"})
	if err != nil || len(rates) != 1 || rates[0].ServiceName != "surface" {
		t.Fatalf("partial failure: rates=%+v err=%v", rates, err)
	}
	if f.chargeReqs[0].WeightGrams != 500 {
		t.Errorf("missing weight must default to 500 g, got %d", f.chargeReqs[0].WeightGrams)
	}

	// Every mode failing is an error.
	f.chargeErrs["Surface"] = errors.New("401")
	if _, err := p.GetRates(context.Background(), integration.RateRequest{FromPostalCode: "201301", ToPostalCode: "560001"}); err == nil {
		t.Error("total failure must be an error")
	}

	// Non-Indian lane: nothing to quote, no API call.
	f.chargeReqs = nil
	rates, err = p.GetRates(context.Background(), integration.RateRequest{FromPostalCode: "00-950", ToPostalCode: "560001"})
	if err != nil || rates != nil || len(f.chargeReqs) != 0 {
		t.Errorf("polish lane: rates=%v err=%v calls=%d", rates, err, len(f.chargeReqs))
	}
}
