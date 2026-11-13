package model

import "testing"

func TestDefaultOrderCurrency(t *testing.T) {
	cases := map[string]string{
		"":      "PLN",
		"inr":   "INR",
		" INR ": "INR",
		"RUPEE": "PLN",
		"I1R":   "PLN",
		"eu":    "PLN",
	}
	for env, want := range cases {
		t.Setenv("OPENOMS_DEFAULT_CURRENCY", env)
		if got := DefaultOrderCurrency(); got != want {
			t.Errorf("OPENOMS_DEFAULT_CURRENCY=%q => %q, want %q", env, got, want)
		}
	}
}

func TestCreateOrderRequestUsesConfiguredDefaultCurrency(t *testing.T) {
	t.Setenv("OPENOMS_DEFAULT_CURRENCY", "INR")
	req := CreateOrderRequest{CustomerName: "Asha Verma"}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if req.Currency != "INR" {
		t.Errorf("currency = %q, want INR", req.Currency)
	}

	explicit := CreateOrderRequest{CustomerName: "Asha Verma", Currency: "EUR"}
	if err := explicit.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if explicit.Currency != "EUR" {
		t.Errorf("an explicit currency must be kept, got %q", explicit.Currency)
	}
}
