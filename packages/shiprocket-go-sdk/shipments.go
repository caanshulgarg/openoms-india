package shiprocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CreateOrder creates a custom ("adhoc") order. Shiprocket returns its own
// order_id and shipment_id; both are needed for later calls.
func (c *Client) CreateOrder(ctx context.Context, req *CreateOrderRequest) (*CreateOrderResponse, error) {
	if req == nil {
		return nil, errors.New("shiprocket: create order: nil request")
	}
	var out CreateOrderResponse
	if err := c.do(ctx, http.MethodPost, "/orders/create/adhoc", nil, req, &out); err != nil {
		return nil, err
	}
	if out.ShipmentID == "" || out.OrderID == "" {
		return nil, fmt.Errorf("shiprocket: create order: response has no order_id/shipment_id (status %q)", out.Status)
	}
	return &out, nil
}

// AssignAWB assigns a courier and AWB number to a shipment. Pass an empty
// courierID to let Shiprocket pick the courier from the account's rules.
func (c *Client) AssignAWB(ctx context.Context, shipmentID, courierID string) (*AWBAssignment, error) {
	payload := map[string]any{"shipment_id": shipmentID}
	if courierID != "" {
		payload["courier_id"] = courierID
	}

	var raw struct {
		AWBAssignStatus int             `json:"awb_assign_status"`
		Message         json.RawMessage `json:"message"`
		Response        json.RawMessage `json:"response"`
	}
	if err := c.do(ctx, http.MethodPost, "/courier/assign/awb", nil, payload, &raw); err != nil {
		return nil, err
	}

	var body struct {
		Data struct {
			AWBCode          FlexString `json:"awb_code"`
			CourierCompanyID FlexString `json:"courier_company_id"`
			CourierName      string     `json:"courier_name"`
			RoutingCode      FlexString `json:"routing_code"`
			AWBAssignError   string     `json:"awb_assign_error"`
		} `json:"data"`
	}
	// "response" is an object on success but may be a plain string on failure.
	_ = json.Unmarshal(raw.Response, &body)

	if raw.AWBAssignStatus != 1 || body.Data.AWBCode == "" {
		reason := body.Data.AWBAssignError
		if reason == "" {
			reason = rawToText(raw.Message)
		}
		if reason == "" {
			reason = rawToText(raw.Response)
		}
		if reason == "" {
			reason = "no AWB returned"
		}
		return nil, fmt.Errorf("shiprocket: assign awb failed: %s", reason)
	}

	return &AWBAssignment{
		AWBCode:          body.Data.AWBCode.String(),
		CourierCompanyID: body.Data.CourierCompanyID.String(),
		CourierName:      body.Data.CourierName,
		RoutingCode:      body.Data.RoutingCode.String(),
	}, nil
}

// GeneratePickup schedules a courier pickup for a shipment that has an AWB.
func (c *Client) GeneratePickup(ctx context.Context, shipmentID string) (*PickupResult, error) {
	payload := map[string]any{"shipment_id": []string{shipmentID}}
	var raw struct {
		PickupStatus int             `json:"pickup_status"`
		Response     json.RawMessage `json:"response"`
	}
	if err := c.do(ctx, http.MethodPost, "/courier/generate/pickup", nil, payload, &raw); err != nil {
		return nil, err
	}
	var body struct {
		PickupScheduledDate string     `json:"pickup_scheduled_date"`
		PickupTokenNumber   FlexString `json:"pickup_token_number"`
	}
	_ = json.Unmarshal(raw.Response, &body)
	return &PickupResult{
		PickupStatus:        raw.PickupStatus,
		PickupScheduledDate: body.PickupScheduledDate,
		PickupTokenNumber:   body.PickupTokenNumber.String(),
	}, nil
}

// GenerateLabel returns the URL of the label PDF for the given shipments.
func (c *Client) GenerateLabel(ctx context.Context, shipmentIDs ...string) (string, error) {
	if len(shipmentIDs) == 0 {
		return "", errors.New("shiprocket: generate label: no shipment ids")
	}
	var out struct {
		LabelCreated int             `json:"label_created"`
		LabelURL     string          `json:"label_url"`
		Response     json.RawMessage `json:"response"`
		NotCreated   json.RawMessage `json:"not_created"`
	}
	if err := c.do(ctx, http.MethodPost, "/courier/generate/label", nil, map[string]any{"shipment_id": shipmentIDs}, &out); err != nil {
		return "", err
	}
	if out.LabelURL == "" {
		reason := rawToText(out.Response)
		if reason == "" {
			reason = "no label_url returned (is an AWB assigned?)"
		}
		return "", fmt.Errorf("shiprocket: generate label failed: %s", reason)
	}
	return out.LabelURL, nil
}

// GenerateManifest generates the manifest for shipments whose pickup has been
// scheduled and returns the manifest PDF URL.
func (c *Client) GenerateManifest(ctx context.Context, shipmentIDs ...string) (string, error) {
	if len(shipmentIDs) == 0 {
		return "", errors.New("shiprocket: generate manifest: no shipment ids")
	}
	var out struct {
		Status      FlexString `json:"status"`
		ManifestURL string     `json:"manifest_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/manifests/generate", nil, map[string]any{"shipment_id": shipmentIDs}, &out); err != nil {
		return "", err
	}
	return out.ManifestURL, nil
}

// PrintManifest returns the manifest PDF URL for already-manifested orders.
func (c *Client) PrintManifest(ctx context.Context, orderIDs ...string) (string, error) {
	if len(orderIDs) == 0 {
		return "", errors.New("shiprocket: print manifest: no order ids")
	}
	var out struct {
		ManifestURL string `json:"manifest_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/manifests/print", nil, map[string]any{"order_ids": orderIDs}, &out); err != nil {
		return "", err
	}
	return out.ManifestURL, nil
}

// PrintInvoice returns the invoice PDF URL for the given Shiprocket order ids.
func (c *Client) PrintInvoice(ctx context.Context, orderIDs ...string) (string, error) {
	if len(orderIDs) == 0 {
		return "", errors.New("shiprocket: print invoice: no order ids")
	}
	var out struct {
		InvoiceURL string `json:"invoice_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/orders/print/invoice", nil, map[string]any{"ids": orderIDs}, &out); err != nil {
		return "", err
	}
	return out.InvoiceURL, nil
}

// TrackByAWB returns the tracking history for an AWB number.
func (c *Client) TrackByAWB(ctx context.Context, awb string) (*TrackingData, error) {
	awb = strings.TrimSpace(awb)
	if awb == "" {
		return nil, errors.New("shiprocket: track: empty awb")
	}
	var out struct {
		TrackingData TrackingData `json:"tracking_data"`
	}
	if err := c.do(ctx, http.MethodGet, "/courier/track/awb/"+url.PathEscape(awb), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.TrackingData, nil
}

// CancelOrders cancels orders by their Shiprocket order ids. This also cancels
// the shipments (and frees the AWBs) attached to them.
func (c *Client) CancelOrders(ctx context.Context, orderIDs ...string) error {
	if len(orderIDs) == 0 {
		return errors.New("shiprocket: cancel: no order ids")
	}
	ids := make([]any, 0, len(orderIDs))
	for _, id := range orderIDs {
		// Shiprocket expects numeric ids; fall back to the raw string otherwise.
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			ids = append(ids, n)
		} else {
			ids = append(ids, id)
		}
	}
	return c.do(ctx, http.MethodPost, "/orders/cancel", nil, map[string]any{"ids": ids}, nil)
}

// Serviceability lists couriers that can ship between two pincodes, with the
// quoted rate of each.
func (c *Client) Serviceability(ctx context.Context, req ServiceabilityRequest) (*ServiceabilityResponse, error) {
	if req.PickupPostcode == "" || req.DeliveryPostcode == "" {
		return nil, errors.New("shiprocket: serviceability: pickup and delivery postcodes are required")
	}
	q := url.Values{}
	q.Set("pickup_postcode", req.PickupPostcode)
	q.Set("delivery_postcode", req.DeliveryPostcode)
	q.Set("weight", strconv.FormatFloat(req.WeightKg, 'f', -1, 64))
	if req.COD {
		q.Set("cod", "1")
	} else {
		q.Set("cod", "0")
	}
	if req.DeclaredValue > 0 {
		q.Set("declared_value", strconv.FormatFloat(req.DeclaredValue, 'f', -1, 64))
	}
	if req.LengthCm > 0 && req.BreadthCm > 0 && req.HeightCm > 0 {
		q.Set("length", strconv.FormatFloat(req.LengthCm, 'f', -1, 64))
		q.Set("breadth", strconv.FormatFloat(req.BreadthCm, 'f', -1, 64))
		q.Set("height", strconv.FormatFloat(req.HeightCm, 'f', -1, 64))
	}

	var out struct {
		Data struct {
			Couriers          []CourierOption `json:"available_courier_companies"`
			RecommendedID     FlexString      `json:"recommended_courier_company_id"`
			ShiprocketRecomID FlexString      `json:"shiprocket_recommended_courier_id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/courier/serviceability/", q, nil, &out); err != nil {
		// Shiprocket answers 404 when no courier serves the lane.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return &ServiceabilityResponse{}, nil
		}
		return nil, err
	}
	recommended := out.Data.RecommendedID.String()
	if recommended == "" {
		recommended = out.Data.ShiprocketRecomID.String()
	}
	return &ServiceabilityResponse{Couriers: out.Data.Couriers, RecommendedCourierID: recommended}, nil
}

// PickupLocations lists the pickup addresses registered in the account.
func (c *Client) PickupLocations(ctx context.Context) ([]PickupLocation, error) {
	var out struct {
		Data struct {
			ShippingAddress []PickupLocation `json:"shipping_address"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/settings/company/pickup", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Data.ShippingAddress, nil
}
