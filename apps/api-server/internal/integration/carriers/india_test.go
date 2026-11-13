package carriers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/openoms-org/openoms/apps/api-server/internal/integration"
)

func TestIsIndianPIN(t *testing.T) {
	valid := []string{"110001", "560001", "999999"}
	invalid := []string{"", "11000", "1100011", "011001", "11000a", "00-950", "110 01"}
	for _, pin := range valid {
		if !isIndianPIN(pin) {
			t.Errorf("isIndianPIN(%q) = false, want true", pin)
		}
	}
	for _, pin := range invalid {
		if isIndianPIN(pin) {
			t.Errorf("isIndianPIN(%q) = true, want false", pin)
		}
	}
	if got := normalizeIndianPIN(" 110 001 "); got != "110001" {
		t.Errorf("normalizeIndianPIN = %q", got)
	}
}

func TestNormalizeIndianPhone(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"9876543210", "9876543210", true},
		{"+91 98765 43210", "9876543210", true},
		{"91-9876543210", "9876543210", true},
		{"09876543210", "9876543210", true},
		{"0091 9876543210", "9876543210", true},
		{"(987) 654-3210", "9876543210", true},
		{"98765", "98765", false},
		{"", "", false},
		{"+48 600 700 800", "48600700800", false},
		{"0123456789", "0123456789", false},
	}
	for _, tc := range cases {
		got, ok := normalizeIndianPhone(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normalizeIndianPhone(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIndiaStateFromPIN(t *testing.T) {
	cases := map[string]string{
		"110001": "Delhi",
		"122001": "Haryana",
		"141001": "Punjab",
		"160017": "Chandigarh",
		"171001": "Himachal Pradesh",
		"190001": "Jammu and Kashmir",
		"194101": "Ladakh",
		"201301": "Uttar Pradesh", // Noida
		"226001": "Uttar Pradesh", // Lucknow
		"248001": "Uttarakhand",   // Dehradun
		"302001": "Rajasthan",
		"380001": "Gujarat",
		"400001": "Maharashtra",
		"403001": "Goa",
		"462001": "Madhya Pradesh",
		"492001": "Chhattisgarh",
		"500001": "Telangana",
		"520001": "Andhra Pradesh",
		"560001": "Karnataka",
		"600001": "Tamil Nadu",
		"605001": "Puducherry",
		"682001": "Kerala",
		"700001": "West Bengal",
		"737101": "Sikkim",
		"744101": "Andaman and Nicobar Islands",
		"751001": "Odisha",
		"781001": "Assam",
		"791111": "Arunachal Pradesh",
		"793001": "Meghalaya",
		"795001": "Manipur",
		"796001": "Mizoram",
		"797001": "Nagaland",
		"799001": "Tripura",
		"800001": "Bihar",
		"834001": "Jharkhand", // Ranchi
		"831001": "Jharkhand", // Jamshedpur
		"900001": "",          // Army Postal Service: no state
		"290001": "",          // unused range
		"abc":    "",
		"00-950": "",
	}
	for pin, want := range cases {
		if got := indiaStateFromPIN(pin); got != want {
			t.Errorf("indiaStateFromPIN(%q) = %q, want %q", pin, got, want)
		}
	}
}

func TestValidateIndiaReceiver(t *testing.T) {
	base := integration.CarrierReceiver{
		Name: " Asha Verma ", Phone: "+91 98765 43210", Street: "12 MG Road",
		City: "Bengaluru", PostalCode: "560 001", Country: "IN",
	}
	got, err := validateIndiaReceiver("x", base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Asha Verma" || got.Phone != "9876543210" || got.PIN != "560001" || got.State != "Karnataka" {
		t.Fatalf("unexpected receiver: %+v", got)
	}

	explicit := base
	explicit.State = "KA"
	if got, _ := validateIndiaReceiver("x", explicit); got.State != "KA" {
		t.Errorf("explicit state must win over the PIN fallback, got %q", got.State)
	}

	for _, country := range []string{"", "in", "India", "IND"} {
		r := base
		r.Country = country
		if _, err := validateIndiaReceiver("x", r); err != nil {
			t.Errorf("country %q should be accepted: %v", country, err)
		}
	}

	bad := map[string]func(*integration.CarrierReceiver){
		"recipient name":    func(r *integration.CarrierReceiver) { r.Name = "  " },
		"recipient address": func(r *integration.CarrierReceiver) { r.Street = "" },
		"within India":      func(r *integration.CarrierReceiver) { r.Country = "PL" },
		"6-digit PIN":       func(r *integration.CarrierReceiver) { r.PostalCode = "00-950" },
		"10-digit Indian":   func(r *integration.CarrierReceiver) { r.Phone = "12345" },
	}
	for want, mutate := range bad {
		r := base
		mutate(&r)
		_, err := validateIndiaReceiver("x", r)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
}

func TestShipmentValueAndCurrency(t *testing.T) {
	items := []integration.CarrierItem{{Name: "A", Quantity: 2, UnitPrice: 100}, {Name: "B", Quantity: 0, UnitPrice: 50}}
	if total, units := carrierItemsValue(items); total != 250 || units != 3 {
		t.Errorf("carrierItemsValue = %v, %v", total, units)
	}
	if v := shipmentValue(integration.CarrierShipmentRequest{DeclaredValue: 999, Items: items, CODAmount: 5}); v != 999 {
		t.Errorf("declared value should win, got %v", v)
	}
	if v := shipmentValue(integration.CarrierShipmentRequest{Items: items, CODAmount: 5}); v != 250 {
		t.Errorf("items total should be second, got %v", v)
	}
	if v := shipmentValue(integration.CarrierShipmentRequest{CODAmount: 5, InsuredValue: 9}); v != 5 {
		t.Errorf("COD should be third, got %v", v)
	}
	if v := shipmentValue(integration.CarrierShipmentRequest{InsuredValue: 9}); v != 9 {
		t.Errorf("insured value should be last, got %v", v)
	}

	for _, ok := range []string{"", "INR", "inr", " INR "} {
		if err := requireINR("x", ok); err != nil {
			t.Errorf("requireINR(%q) = %v", ok, err)
		}
	}
	if err := requireINR("x", "PLN"); err == nil {
		t.Error("requireINR(PLN) must fail")
	}
	if got := truncateRunes("żółć-ąę", 4); got != "żółć" {
		t.Errorf("truncateRunes = %q", got)
	}
}

func TestSettingFloat(t *testing.T) {
	ok := map[string]float64{
		`{"w":0.5}`: 0.5, `{"w":"0.5"}`: 0.5, `{"w":" 1,25 "}`: 1.25, `{"w":""}`: 0, `{"w":null}`: 0, `{}`: 0, `{"w":2}`: 2,
	}
	for in, want := range ok {
		var v struct {
			W settingFloat `json:"w"`
		}
		if err := json.Unmarshal([]byte(in), &v); err != nil || float64(v.W) != want {
			t.Errorf("%s => %v, %v; want %v", in, v.W, err, want)
		}
	}
	for _, in := range []string{`{"w":"abc"}`, `{"w":-1}`, `{"w":"-2"}`, `{"w":true}`, `{"w":{}}`} {
		var v struct {
			W settingFloat `json:"w"`
		}
		if err := json.Unmarshal([]byte(in), &v); err == nil {
			t.Errorf("%s must be rejected, got %v", in, v.W)
		}
	}
}

func TestAsCarrierUserError(t *testing.T) {
	if asCarrierUserError(nil) != nil {
		t.Fatal("nil must stay nil")
	}

	var userErr *integration.CarrierUserError
	plain := errors.New("delhivery: parcel weight is required")
	marked := asCarrierUserError(plain)
	if !errors.As(marked, &userErr) || marked.Error() != plain.Error() || !errors.Is(marked, plain) {
		t.Fatalf("validation error must be marked and keep its message: %v", marked)
	}
	if again := asCarrierUserError(marked); again != marked {
		t.Error("an already marked error must not be wrapped twice")
	}
	// The mark must survive the service layer's wrapping.
	if !errors.As(fmt.Errorf("carrier create shipment: %w", marked), &userErr) {
		t.Error("mark lost after wrapping")
	}

	transport := []error{
		&url.Error{Op: "Post", URL: "https://track.delhivery.com/api/cmu/create.json", Err: errors.New("dial tcp: i/o timeout")},
		fmt.Errorf("delhivery: create shipment: %w", context.DeadlineExceeded),
		context.Canceled,
	}
	for _, err := range transport {
		if errors.As(asCarrierUserError(err), &userErr) {
			t.Errorf("transport error must not be user-facing: %v", err)
		}
	}
}

func TestProvidersMarkLabelErrorsAsUserFacing(t *testing.T) {
	var userErr *integration.CarrierUserError

	sr := newShiprocketTestProvider(&fakeShiprocket{}, shiprocketSettings{})
	_, err := sr.CreateShipment(context.Background(), shiprocketSampleRequest())
	if !errors.As(err, &userErr) || !strings.Contains(userErr.Error(), "pickup location is not configured") {
		t.Errorf("shiprocket validation error not user-facing: %v", err)
	}
	sr = newShiprocketTestProvider(&fakeShiprocket{awbErr: errors.New("Recharge your wallet")}, shiprocketSettings{PickupLocation: "x"})
	if _, err = sr.CreateShipment(context.Background(), shiprocketSampleRequest()); !errors.As(err, &userErr) {
		t.Errorf("shiprocket carrier rejection not user-facing: %v", err)
	}
	if _, err = sr.GetLabel(context.Background(), "1:2", "zpl"); !errors.As(err, &userErr) {
		t.Errorf("shiprocket label format error not user-facing: %v", err)
	}

	dl := newDelhiveryTestProvider(&fakeDelhivery{}, delhiverySettings{PickupLocation: "x"})
	req := delhiverySampleRequest()
	req.Receiver.PostalCode = "00-950"
	if _, err = dl.CreateShipment(context.Background(), req); !errors.As(err, &userErr) {
		t.Errorf("delhivery validation error not user-facing: %v", err)
	}
	timeout := &url.Error{Op: "Post", URL: "https://track.delhivery.com", Err: context.DeadlineExceeded}
	dl = newDelhiveryTestProvider(&fakeDelhivery{createErr: timeout}, delhiverySettings{PickupLocation: "x"})
	if _, err = dl.CreateShipment(context.Background(), delhiverySampleRequest()); err == nil || errors.As(err, &userErr) {
		t.Errorf("delhivery timeout must stay a plain error: %v", err)
	}
}
