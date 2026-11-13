package delhivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CreateShipment manifests one or more forward shipments.
//
// The API takes a form-encoded body "format=json&data=<json>". A call can
// "succeed" at HTTP level while individual packages fail, so the result is
// checked package by package: an error is returned unless every package was
// created and has a waybill.
func (c *Client) CreateShipment(ctx context.Context, req *CreateRequest) (*CreateResponse, error) {
	if req == nil || len(req.Shipments) == 0 {
		return nil, errors.New("delhivery: create shipment: no shipments")
	}
	if strings.TrimSpace(req.PickupLocation.Name) == "" {
		return nil, errors.New("delhivery: create shipment: pickup_location name is required")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("delhivery: marshal create request: %w", err)
	}
	form := url.Values{}
	form.Set("format", "json")
	form.Set("data", string(data))

	var out CreateResponse
	if err := c.do(ctx, http.MethodPost, "/api/cmu/create.json", nil,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", &out); err != nil {
		return nil, err
	}

	if len(out.Packages) == 0 {
		reason := string(out.Remark)
		if reason == "" {
			reason = "no packages returned"
		}
		return &out, fmt.Errorf("delhivery: create shipment failed: %s", reason)
	}
	for _, pkg := range out.Packages {
		if strings.EqualFold(pkg.Status, "Success") && pkg.Waybill != "" {
			continue
		}
		reason := string(pkg.Remarks)
		if reason == "" {
			reason = string(out.Remark)
		}
		if reason == "" {
			reason = "status " + strconv.Quote(pkg.Status)
		}
		return &out, fmt.Errorf("delhivery: create shipment failed for order %q: %s", pkg.RefNum, reason)
	}
	return &out, nil
}

// PackingSlip returns the label PDF link for a waybill. pdfSize is "A4" or
// "4R" (4x6 thermal); empty uses the account default.
func (c *Client) PackingSlip(ctx context.Context, waybill, pdfSize string) (*PackingSlip, error) {
	waybill = strings.TrimSpace(waybill)
	if waybill == "" {
		return nil, errors.New("delhivery: packing slip: empty waybill")
	}
	q := url.Values{}
	q.Set("wbns", waybill)
	q.Set("pdf", "true")
	if pdfSize != "" {
		q.Set("pdf_size", pdfSize)
	}
	var out struct {
		Packages []struct {
			PDFDownloadLink string `json:"pdf_download_link"`
		} `json:"packages"`
		PackagesFound int `json:"packages_found"`
	}
	if err := c.getJSON(ctx, "/api/p/packing_slip", q, &out); err != nil {
		return nil, err
	}
	for _, pkg := range out.Packages {
		if pkg.PDFDownloadLink != "" {
			return &PackingSlip{PDFDownloadLink: pkg.PDFDownloadLink, PackagesFound: out.PackagesFound}, nil
		}
	}
	return nil, fmt.Errorf("delhivery: packing slip: no label found for waybill %q", waybill)
}

// Track returns the tracking record of each requested waybill (max 50 per
// call per Delhivery's limits; the pull API is rate-limited per IP).
func (c *Client) Track(ctx context.Context, waybills ...string) ([]TrackedShipment, error) {
	cleaned := make([]string, 0, len(waybills))
	for _, w := range waybills {
		if w = strings.TrimSpace(w); w != "" {
			cleaned = append(cleaned, w)
		}
	}
	if len(cleaned) == 0 {
		return nil, errors.New("delhivery: track: no waybills")
	}
	q := url.Values{}
	q.Set("waybill", strings.Join(cleaned, ","))

	var out struct {
		ShipmentData []struct {
			Shipment TrackedShipment `json:"Shipment"`
		} `json:"ShipmentData"`
		Error   json.RawMessage `json:"Error"`
		Success *bool           `json:"Success"`
	}
	if err := c.getJSON(ctx, "/api/v1/packages/json/", q, &out); err != nil {
		return nil, err
	}
	if len(out.ShipmentData) == 0 {
		if msg := rawToText(out.Error); msg != "" {
			return nil, fmt.Errorf("delhivery: track: %s", msg)
		}
		return nil, nil
	}
	shipments := make([]TrackedShipment, 0, len(out.ShipmentData))
	for _, sd := range out.ShipmentData {
		shipments = append(shipments, sd.Shipment)
	}
	return shipments, nil
}

// Cancel cancels a shipment. Delhivery only allows this while the package is
// Manifested, In Transit, Pending, Open or Scheduled; a forward package that
// has already been picked up becomes a return (RTO).
func (c *Client) Cancel(ctx context.Context, waybill string) error {
	waybill = strings.TrimSpace(waybill)
	if waybill == "" {
		return errors.New("delhivery: cancel: empty waybill")
	}
	var out struct {
		Status json.RawMessage `json:"status"`
		Remark json.RawMessage `json:"remark"`
		Error  json.RawMessage `json:"error"`
	}
	if err := c.postJSON(ctx, "/api/p/edit", map[string]string{"waybill": waybill, "cancellation": "true"}, &out); err != nil {
		return err
	}
	if !truthy(out.Status) {
		reason := rawToText(out.Error)
		if reason == "" {
			reason = rawToText(out.Remark)
		}
		if reason == "" {
			reason = "cancellation was not confirmed"
		}
		return fmt.Errorf("delhivery: cancel failed: %s", reason)
	}
	return nil
}

// truthy reports whether a JSON status value means success: true, "true",
// "Success" (any case) or 1.
func truthy(raw json.RawMessage) bool {
	s := strings.ToLower(strings.Trim(string(bytes.TrimSpace(raw)), `"`))
	return s == "true" || s == "success" || s == "1"
}

// CreatePickupRequest asks Delhivery to collect packages from a warehouse.
func (c *Client) CreatePickupRequest(ctx context.Context, req PickupRequest) (*PickupResponse, error) {
	if strings.TrimSpace(req.PickupLocation) == "" || req.PickupDate == "" || req.PickupTime == "" {
		return nil, errors.New("delhivery: pickup request: pickup_location, pickup_date and pickup_time are required")
	}
	if req.ExpectedPackageCount < 1 {
		req.ExpectedPackageCount = 1
	}
	var raw map[string]json.RawMessage
	if err := c.postJSON(ctx, "/fm/request/new/", req, &raw); err != nil {
		return nil, err
	}
	var id FlexString
	_ = json.Unmarshal(raw["pickup_id"], &id)
	if id == "" {
		reason := rawToText(raw["error"])
		if reason == "" {
			reason = rawToText(raw["message"])
		}
		if reason == "" {
			if b, err := json.Marshal(raw); err == nil {
				reason = truncate(string(b), 300)
			}
		}
		return nil, fmt.Errorf("delhivery: pickup request failed: %s", reason)
	}
	return &PickupResponse{
		PickupID:           id.String(),
		IncomingCenterName: rawToText(raw["incoming_center_name"]),
	}, nil
}

// ShippingCharges returns Delhivery's estimated charge for a lane. The amount
// actually billed can differ (final weight, zone, surcharges).
func (c *Client) ShippingCharges(ctx context.Context, req ChargesRequest) ([]Charge, error) {
	if req.OriginPin == "" || req.DestinationPin == "" {
		return nil, errors.New("delhivery: shipping charges: origin and destination pins are required")
	}
	q := url.Values{}
	if req.Mode == ModeExpress {
		q.Set("md", "E")
	} else {
		q.Set("md", "S")
	}
	status := req.ShipmentStatus
	if status == "" {
		status = "Delivered"
	}
	q.Set("ss", status)
	q.Set("o_pin", req.OriginPin)
	q.Set("d_pin", req.DestinationPin)
	q.Set("cgm", strconv.Itoa(req.WeightGrams))
	if req.COD {
		q.Set("pt", "COD")
		q.Set("cod", strconv.FormatFloat(req.CODAmount, 'f', -1, 64))
	} else {
		q.Set("pt", "Pre-paid")
	}

	var raw json.RawMessage
	if err := c.getJSON(ctx, "/api/kinko/v1/invoice/charges/.json", q, &raw); err != nil {
		return nil, err
	}
	var charges []Charge
	if err := json.Unmarshal(raw, &charges); err != nil {
		// A non-array body is an error document.
		return nil, fmt.Errorf("delhivery: shipping charges: unexpected response: %s", truncate(string(raw), 300))
	}
	return charges, nil
}

// PincodeServiceability returns Delhivery's serviceability of a pincode, or
// nil when the pincode is not serviced at all.
func (c *Client) PincodeServiceability(ctx context.Context, pin string) (*PincodeInfo, error) {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return nil, errors.New("delhivery: pincode serviceability: empty pin")
	}
	q := url.Values{}
	q.Set("filter_codes", pin)
	var out struct {
		DeliveryCodes []struct {
			PostalCode struct {
				Pin       FlexString `json:"pin"`
				City      string     `json:"city"`
				District  string     `json:"district"`
				StateCode string     `json:"state_code"`
				PrePaid   string     `json:"pre_paid"`
				COD       string     `json:"cod"`
				Pickup    string     `json:"pickup"`
				Repl      string     `json:"repl"`
				IsODA     string     `json:"is_oda"`
				Remarks   string     `json:"remarks"`
			} `json:"postal_code"`
		} `json:"delivery_codes"`
	}
	if err := c.getJSON(ctx, "/c/api/pin-codes/json/", q, &out); err != nil {
		return nil, err
	}
	for _, dc := range out.DeliveryCodes {
		pc := dc.PostalCode
		if pc.Pin.String() != pin {
			continue
		}
		return &PincodeInfo{
			Pin:         pc.Pin.String(),
			City:        pc.City,
			District:    pc.District,
			StateCode:   pc.StateCode,
			Prepaid:     yes(pc.PrePaid),
			COD:         yes(pc.COD),
			Pickup:      yes(pc.Pickup),
			Replacement: yes(pc.Repl),
			ODA:         yes(pc.IsODA),
			Remarks:     pc.Remarks,
		}, nil
	}
	return nil, nil
}

func yes(flag string) bool { return strings.EqualFold(strings.TrimSpace(flag), "Y") }
