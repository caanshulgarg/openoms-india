package service

import (
	"encoding/json"
	"testing"
)

func TestCarrierItemsFromOrder(t *testing.T) {
	items := carrierItemsFromOrder(json.RawMessage(
		`[{"name":"Shirt","sku":"S1","quantity":2,"price":499},{"name":"","sku":"","quantity":1,"price":5},{"name":"Cap","quantity":0,"price":199.5}]`))
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (nameless line dropped): %+v", len(items), items)
	}
	if items[0].Name != "Shirt" || items[0].SKU != "S1" || items[0].Quantity != 2 || items[0].UnitPrice != 499 {
		t.Errorf("unexpected first item: %+v", items[0])
	}
	if items[1].Name != "Cap" || items[1].Quantity != 1 || items[1].UnitPrice != 199.5 {
		t.Errorf("zero quantity must become 1: %+v", items[1])
	}

	for _, raw := range []string{"", "null", "{}", "not json", "[]"} {
		if got := carrierItemsFromOrder(json.RawMessage(raw)); len(got) != 0 {
			t.Errorf("carrierItemsFromOrder(%q) = %+v, want none", raw, got)
		}
	}
}
