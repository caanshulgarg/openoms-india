package shiprocket

import "strings"

// statusMapping maps normalised Shiprocket shipment statuses to OpenOMS
// shipment statuses (picked_up, in_transit, out_for_delivery, delivered,
// returned, failed).
//
// Deliberately unmapped:
//   - pre-pickup states (AWB ASSIGNED, LABEL GENERATED, PICKUP SCHEDULED, ...):
//     the shipment stays at label_ready in OpenOMS;
//   - CANCELED: OpenOMS has no cancelled shipment status;
//   - RTO INITIATED / RTO IN TRANSIT / RTO NDR / RTO OFD: the parcel is still
//     travelling back, "returned" is only set once the seller has it
//     (RTO DELIVERED / RTO ACKNOWLEDGED).
var statusMapping = map[string]string{
	"PICKED UP": "picked_up",

	"SHIPPED":                    "in_transit",
	"IN TRANSIT":                 "in_transit",
	"IN FLIGHT":                  "in_transit",
	"REACHED AT DESTINATION HUB": "in_transit",
	"REACHED DESTINATION HUB":    "in_transit",
	"MISROUTED":                  "in_transit",
	"DELAYED":                    "in_transit",
	"CUSTOM CLEARED":             "in_transit",
	"HANDOVER TO COURIER":        "in_transit",
	"UNDELIVERED":                "in_transit", // NDR: courier will re-attempt or RTO

	"OUT FOR DELIVERY": "out_for_delivery",

	"DELIVERED":         "delivered",
	"PARTIAL DELIVERED": "delivered",

	"RTO DELIVERED":    "returned",
	"RTO ACKNOWLEDGED": "returned",

	"LOST":         "failed",
	"DAMAGED":      "failed",
	"DESTROYED":    "failed",
	"DISPOSED OFF": "failed",
}

// NormalizeStatus upper-cases a status and folds "_" / "-" and repeated
// whitespace into single spaces ("rto_delivered" -> "RTO DELIVERED").
func NormalizeStatus(status string) string {
	s := strings.ToUpper(status)
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// MapStatus translates a Shiprocket shipment status to the OpenOMS shipment
// status. ok is false for statuses that should not change the OMS shipment.
func MapStatus(status string) (openomsStatus string, ok bool) {
	openomsStatus, ok = statusMapping[NormalizeStatus(status)]
	return
}
