package worker

// shipmentForwardChain is the happy-path order of shipment statuses. The
// order engine only allows moving one step at a time along it.
var shipmentForwardChain = []string{"label_ready", "picked_up", "in_transit", "out_for_delivery", "delivered"}

func shipmentChainIndex(status string) int {
	for i, s := range shipmentForwardChain {
		if s == status {
			return i
		}
	}
	return -1
}

// shipmentTransitionPath returns the statuses to apply, in order, to bring a
// shipment from its current status to the status reported by the carrier.
//
// Tracking is polled, so a parcel often advances several steps between two
// polls (label_ready -> in_transit, in_transit -> delivered). The order engine
// rejects such jumps, which used to leave the shipment stuck at its old status
// forever. The path walks the intermediate steps instead:
//
//	label_ready -> delivered  => picked_up, in_transit, out_for_delivery, delivered
//	label_ready -> returned   => picked_up, in_transit, returned
//
// Any other pair is returned as a single direct step and left to the engine to
// accept or reject, exactly as before.
func shipmentTransitionPath(from, to string) []string {
	if from == to {
		return nil
	}
	fromIdx := shipmentChainIndex(from)
	if fromIdx < 0 {
		return []string{to}
	}

	if toIdx := shipmentChainIndex(to); toIdx > fromIdx {
		path := make([]string, 0, toIdx-fromIdx)
		path = append(path, shipmentForwardChain[fromIdx+1:toIdx+1]...)
		return path
	}

	// A return is only reachable from in_transit onwards: catch up to
	// in_transit first when the shipment is still before it.
	if to == "returned" {
		inTransit := shipmentChainIndex("in_transit")
		if fromIdx < inTransit {
			path := make([]string, 0, inTransit-fromIdx+1)
			path = append(path, shipmentForwardChain[fromIdx+1:inTransit+1]...)
			return append(path, to)
		}
	}
	return []string{to}
}
