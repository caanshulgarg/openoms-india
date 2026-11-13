package delhivery

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// FlexString decodes a JSON string, number, bool or null into a string.
// Delhivery returns pins, waybills and ids as numbers on some endpoints.
type FlexString string

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	if data[0] == '{' || data[0] == '[' {
		*f = ""
		return nil
	}
	*f = FlexString(string(data))
	return nil
}

// String returns the underlying string.
func (f FlexString) String() string { return string(f) }

// FlexFloat decodes a JSON number or numeric string into a float64.
// Non-numeric values decode to 0.
type FlexFloat float64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexFloat) UnmarshalJSON(data []byte) error {
	s := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil //nolint:nilerr // tolerant decode by design
	}
	*f = FlexFloat(v)
	return nil
}

// Remarks decodes the "remarks" field, which Delhivery sends either as a
// string or as an array of strings.
type Remarks string

// UnmarshalJSON implements json.Unmarshaler.
func (r *Remarks) UnmarshalJSON(data []byte) error {
	*r = Remarks(rawToText(data))
	return nil
}

// Payment modes accepted by the order creation API.
const (
	PaymentPrepaid = "Prepaid"
	PaymentCOD     = "COD"
	PaymentPickup  = "Pickup" // reverse pickup
	PaymentREPL    = "REPL"   // replacement
)

// Shipping modes.
const (
	ModeSurface = "Surface"
	ModeExpress = "Express"
)

// Shipment is one package in an order creation request.
//
// Weight is in GRAMS; dimensions are in centimetres. Leave Waybill empty to
// have Delhivery assign one.
type Shipment struct {
	Name          string  `json:"name"`
	Add           string  `json:"add"`
	Pin           string  `json:"pin"`
	City          string  `json:"city,omitempty"`
	State         string  `json:"state,omitempty"`
	Country       string  `json:"country"`
	Phone         string  `json:"phone"`
	Order         string  `json:"order"`
	PaymentMode   string  `json:"payment_mode"`
	CODAmount     float64 `json:"cod_amount"`
	TotalAmount   float64 `json:"total_amount"`
	ProductsDesc  string  `json:"products_desc,omitempty"`
	HSNCode       string  `json:"hsn_code,omitempty"`
	Quantity      int     `json:"quantity,omitempty"`
	OrderDate     string  `json:"order_date,omitempty"`
	Waybill       string  `json:"waybill"`
	Weight        float64 `json:"weight,omitempty"`
	Width         float64 `json:"shipment_width,omitempty"`
	Height        float64 `json:"shipment_height,omitempty"`
	Length        float64 `json:"shipment_length,omitempty"`
	ShippingMode  string  `json:"shipping_mode,omitempty"`
	AddressType   string  `json:"address_type,omitempty"`
	SellerName    string  `json:"seller_name,omitempty"`
	SellerAdd     string  `json:"seller_add,omitempty"`
	SellerInv     string  `json:"seller_inv,omitempty"`
	SellerGSTTIN  string  `json:"seller_gst_tin,omitempty"`
	ReturnName    string  `json:"return_name,omitempty"`
	ReturnAdd     string  `json:"return_add,omitempty"`
	ReturnCity    string  `json:"return_city,omitempty"`
	ReturnState   string  `json:"return_state,omitempty"`
	ReturnPin     string  `json:"return_pin,omitempty"`
	ReturnPhone   string  `json:"return_phone,omitempty"`
	ReturnCountry string  `json:"return_country,omitempty"`
	Fragile       bool    `json:"fragile_shipment,omitempty"`
	EwaybillNo    string  `json:"ewbn,omitempty"`
}

// PickupLocationRef names the registered warehouse the shipment ships from.
// Name must match the warehouse name in the Delhivery account exactly
// (case-sensitive).
type PickupLocationRef struct {
	Name string `json:"name"`
}

// CreateRequest is the "data" document of the order creation API.
type CreateRequest struct {
	Shipments      []Shipment        `json:"shipments"`
	PickupLocation PickupLocationRef `json:"pickup_location"`
}

// CreatedPackage is the per-package result of an order creation call.
type CreatedPackage struct {
	Status      string     `json:"status"`
	Waybill     FlexString `json:"waybill"`
	RefNum      FlexString `json:"refnum"`
	Remarks     Remarks    `json:"remarks"`
	Client      string     `json:"client"`
	SortCode    string     `json:"sort_code"`
	Payment     string     `json:"payment"`
	CODAmount   FlexFloat  `json:"cod_amount"`
	Serviceable *bool      `json:"serviceable"`
}

// CreateResponse is returned by the order creation API.
type CreateResponse struct {
	Success      bool             `json:"success"`
	Error        bool             `json:"error"`
	Remark       Remarks          `json:"rmk"`
	UploadWBN    string           `json:"upload_wbn"`
	PackageCount int              `json:"package_count"`
	Packages     []CreatedPackage `json:"packages"`
}

// PackingSlip is the label lookup result.
type PackingSlip struct {
	// PDFDownloadLink is a pre-signed link to the label PDF.
	PDFDownloadLink string
	PackagesFound   int
}

// TrackStatus is the current status block of a tracked shipment.
type TrackStatus struct {
	Status         string `json:"Status"`
	StatusType     string `json:"StatusType"`
	StatusCode     string `json:"StatusCode"`
	StatusDateTime string `json:"StatusDateTime"`
	StatusLocation string `json:"StatusLocation"`
	Instructions   string `json:"Instructions"`
	ReceivedBy     string `json:"RecievedBy"` // sic: Delhivery's spelling
}

// ScanDetail is one scan event.
type ScanDetail struct {
	Scan            string `json:"Scan"`
	ScanType        string `json:"ScanType"`
	ScanDateTime    string `json:"ScanDateTime"`
	StatusDateTime  string `json:"StatusDateTime"`
	ScannedLocation string `json:"ScannedLocation"`
	Instructions    string `json:"Instructions"`
	StatusCode      string `json:"StatusCode"`
}

// TrackedShipment is the tracking record of one waybill.
type TrackedShipment struct {
	AWB                  FlexString  `json:"AWB"`
	ReferenceNo          FlexString  `json:"ReferenceNo"`
	OrderType            string      `json:"OrderType"`
	Origin               string      `json:"Origin"`
	Destination          string      `json:"Destination"`
	PickUpDate           string      `json:"PickUpDate"`
	ExpectedDeliveryDate string      `json:"ExpectedDeliveryDate"`
	DeliveryDate         string      `json:"DeliveryDate"`
	ReturnedDate         string      `json:"ReturnedDate"`
	CODAmount            FlexFloat   `json:"CODAmount"`
	Status               TrackStatus `json:"Status"`
	Scans                []struct {
		ScanDetail ScanDetail `json:"ScanDetail"`
	} `json:"Scans"`
}

// PickupRequest schedules a courier pickup at a registered warehouse.
type PickupRequest struct {
	PickupLocation       string `json:"pickup_location"`        // warehouse name
	PickupDate           string `json:"pickup_date"`            // "2006-01-02"
	PickupTime           string `json:"pickup_time"`            // "15:04:05"
	ExpectedPackageCount int    `json:"expected_package_count"` // >= 1
}

// PickupResponse is returned by the pickup request API.
type PickupResponse struct {
	PickupID           string
	IncomingCenterName string
}

// ChargesRequest is the query of the shipping charge calculator.
type ChargesRequest struct {
	Mode           string  // ModeSurface or ModeExpress
	OriginPin      string  // 6 digits
	DestinationPin string  // 6 digits
	WeightGrams    int     // chargeable weight
	COD            bool    // payment type
	CODAmount      float64 // amount to collect when COD
	ShipmentStatus string  // "Delivered" (default), "RTO" or "DTO"
}

// Charge is one quote returned by the shipping charge calculator.
type Charge struct {
	TotalAmount   FlexFloat `json:"total_amount"`
	GrossAmount   FlexFloat `json:"gross_amount"`
	ChargeCOD     FlexFloat `json:"charge_COD"`
	ChargedWeight FlexFloat `json:"charged_weight"`
	Zone          string    `json:"zone"`
	Status        string    `json:"status"`
}

// PincodeInfo describes Delhivery's serviceability of one pincode.
type PincodeInfo struct {
	Pin         string
	City        string
	District    string
	StateCode   string
	Prepaid     bool
	COD         bool
	Pickup      bool
	Replacement bool
	ODA         bool // out-of-delivery-area surcharge applies
	Remarks     string
}

// ist is Indian Standard Time; Delhivery timestamps carry no zone.
var ist = time.FixedZone("IST", 5*3600+30*60)

// ParseTime parses a Delhivery timestamp. Timestamps without a zone are
// interpreted as IST. The zero time is returned for unparseable input.
func ParseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, ist); err == nil {
			return t
		}
	}
	return time.Time{}
}
