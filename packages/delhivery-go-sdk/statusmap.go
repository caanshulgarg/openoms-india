package delhivery

import "strings"

// Delhivery reports a shipment state as a pair: StatusType (UD = forward /
// undelivered, DL = closed, RT = returning, PP/PU = reverse pickup, CN =
// cancelled) and Status (Manifested, In Transit, Pending, Dispatched,
// Delivered, RTO, DTO, ...). The same Status word means different things
// under different types, so both are needed to map a state.

// StatusKey joins a StatusType and Status into the single string used as the
// tracking event status ("UD:In Transit").
func StatusKey(statusType, status string) string {
	return strings.ToUpper(strings.TrimSpace(statusType)) + ":" + strings.TrimSpace(status)
}

// SplitStatusKey is the inverse of StatusKey. A key without a type prefix is
// returned as ("", key).
func SplitStatusKey(key string) (statusType, status string) {
	if before, after, found := strings.Cut(key, ":"); found && len(before) <= 3 {
		return strings.ToUpper(strings.TrimSpace(before)), strings.TrimSpace(after)
	}
	return "", strings.TrimSpace(key)
}

// MapStatus translates a Delhivery (StatusType, Status) pair to the OpenOMS
// shipment status (in_transit, out_for_delivery, delivered, returned, failed).
//
// Deliberately unmapped (ok = false, the OMS shipment is left unchanged):
//   - UD Manifested / Not Picked: not yet picked up;
//   - RT *: the parcel is travelling back to the seller, "returned" is only
//     set once it has arrived (DL RTO);
//   - CN and reverse-pickup flows (PP, PU, DL DTO).
func MapStatus(statusType, status string) (openomsStatus string, ok bool) {
	t := strings.ToUpper(strings.TrimSpace(statusType))
	s := strings.ToUpper(strings.Join(strings.Fields(status), " "))

	if s == "LOST" {
		return "failed", true
	}

	switch t {
	case "UD":
		switch s {
		case "IN TRANSIT", "PENDING":
			return "in_transit", true
		case "DISPATCHED":
			return "out_for_delivery", true
		}
	case "DL":
		switch s {
		case "DELIVERED":
			return "delivered", true
		case "RTO":
			return "returned", true
		}
	}
	return "", false
}

// MapStatusKey maps a key produced by StatusKey.
func MapStatusKey(key string) (openomsStatus string, ok bool) {
	t, s := SplitStatusKey(key)
	return MapStatus(t, s)
}
