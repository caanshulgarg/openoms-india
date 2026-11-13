package worker

import (
	"strings"
	"testing"
)

func TestShipmentTransitionPath(t *testing.T) {
	cases := []struct {
		from, to string
		want     string
	}{
		{"label_ready", "picked_up", "picked_up"},
		{"label_ready", "in_transit", "picked_up,in_transit"},
		{"label_ready", "out_for_delivery", "picked_up,in_transit,out_for_delivery"},
		{"label_ready", "delivered", "picked_up,in_transit,out_for_delivery,delivered"},
		{"picked_up", "in_transit", "in_transit"},
		{"picked_up", "delivered", "in_transit,out_for_delivery,delivered"},
		{"in_transit", "out_for_delivery", "out_for_delivery"},
		{"in_transit", "delivered", "out_for_delivery,delivered"},
		{"out_for_delivery", "delivered", "delivered"},

		{"label_ready", "returned", "picked_up,in_transit,returned"},
		{"picked_up", "returned", "in_transit,returned"},
		{"in_transit", "returned", "returned"},
		{"out_for_delivery", "returned", "returned"},
		{"delivered", "returned", "returned"},

		// Failures are a direct step from any active status.
		{"label_ready", "failed", "failed"},
		{"in_transit", "failed", "failed"},

		// Backwards or unknown pairs are passed through for the engine to judge.
		{"out_for_delivery", "in_transit", "in_transit"},
		{"created", "in_transit", "in_transit"},
		{"generating_label", "delivered", "delivered"},
		{"in_transit", "cancelled", "cancelled"},

		{"in_transit", "in_transit", ""},
	}
	for _, tc := range cases {
		got := strings.Join(shipmentTransitionPath(tc.from, tc.to), ",")
		if got != tc.want {
			t.Errorf("shipmentTransitionPath(%q, %q) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestShipmentTransitionPathDoesNotAliasChain(t *testing.T) {
	path := shipmentTransitionPath("label_ready", "returned")
	path[0] = "mutated"
	if shipmentForwardChain[1] != "picked_up" {
		t.Fatal("path must not share memory with shipmentForwardChain")
	}
}
