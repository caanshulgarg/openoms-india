package shiprocket

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// FlexString decodes a JSON string, number, bool or null into a string.
// Shiprocket returns identifiers (order_id, shipment_id, awb_code, courier
// ids) as numbers on some endpoints and as strings on others.
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
		*f = "" // unexpected structured value: ignore rather than fail the call
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

// OrderItem is one line of a Shiprocket order.
type OrderItem struct {
	Name         string  `json:"name"`
	SKU          string  `json:"sku"`
	Units        int     `json:"units"`
	SellingPrice float64 `json:"selling_price"`
	Discount     float64 `json:"discount,omitempty"`
	Tax          float64 `json:"tax,omitempty"`
	HSN          string  `json:"hsn,omitempty"`
}

// CreateOrderRequest is the payload of POST /orders/create/adhoc.
//
// Weight is in kilograms and dimensions are in centimetres. PickupLocation is
// the nickname of a pickup address registered in the Shiprocket account.
type CreateOrderRequest struct {
	OrderID              string      `json:"order_id"`
	OrderDate            string      `json:"order_date"` // "2006-01-02 15:04"
	PickupLocation       string      `json:"pickup_location"`
	ChannelID            string      `json:"channel_id,omitempty"`
	Comment              string      `json:"comment,omitempty"`
	BillingCustomerName  string      `json:"billing_customer_name"`
	BillingLastName      string      `json:"billing_last_name"`
	BillingAddress       string      `json:"billing_address"`
	BillingAddress2      string      `json:"billing_address_2,omitempty"`
	BillingCity          string      `json:"billing_city"`
	BillingPincode       string      `json:"billing_pincode"`
	BillingState         string      `json:"billing_state"`
	BillingCountry       string      `json:"billing_country"`
	BillingEmail         string      `json:"billing_email,omitempty"`
	BillingPhone         string      `json:"billing_phone"`
	ShippingIsBilling    bool        `json:"shipping_is_billing"`
	OrderItems           []OrderItem `json:"order_items"`
	PaymentMethod        string      `json:"payment_method"` // "Prepaid" or "COD"
	ShippingCharges      float64     `json:"shipping_charges,omitempty"`
	TotalDiscount        float64     `json:"total_discount,omitempty"`
	SubTotal             float64     `json:"sub_total"`
	Length               float64     `json:"length"`
	Breadth              float64     `json:"breadth"`
	Height               float64     `json:"height"`
	Weight               float64     `json:"weight"`
	EwaybillNo           string      `json:"ewaybill_no,omitempty"`
	CustomerGSTIN        string      `json:"customer_gstin,omitempty"`
	InvoiceNumber        string      `json:"invoice_number,omitempty"`
	OrderType            string      `json:"order_type,omitempty"`
	ResellerName         string      `json:"reseller_name,omitempty"`
	CompanyName          string      `json:"company_name,omitempty"`
	ShippingCustomerName string      `json:"shipping_customer_name,omitempty"`
}

// CreateOrderResponse is returned by POST /orders/create/adhoc.
type CreateOrderResponse struct {
	OrderID          FlexString `json:"order_id"`
	ShipmentID       FlexString `json:"shipment_id"`
	Status           string     `json:"status"`
	StatusCode       int        `json:"status_code"`
	AWBCode          FlexString `json:"awb_code"`
	CourierCompanyID FlexString `json:"courier_company_id"`
	CourierName      string     `json:"courier_name"`
}

// AWBAssignment is the result of POST /courier/assign/awb.
type AWBAssignment struct {
	AWBCode          string
	CourierCompanyID string
	CourierName      string
	RoutingCode      string
}

// PickupResult is the result of POST /courier/generate/pickup.
type PickupResult struct {
	PickupStatus        int
	PickupScheduledDate string
	PickupTokenNumber   string
}

// TrackActivity is one scan in a tracking history.
type TrackActivity struct {
	Date          string     `json:"date"` // "2006-01-02 15:04:05" in IST
	Status        string     `json:"status"`
	Activity      string     `json:"activity"`
	Location      string     `json:"location"`
	SRStatus      FlexString `json:"sr-status"`
	SRStatusLabel string     `json:"sr-status-label"`
}

// ShipmentTrack is the per-shipment summary of a tracking response.
type ShipmentTrack struct {
	AWBCode       FlexString `json:"awb_code"`
	ShipmentID    FlexString `json:"shipment_id"`
	OrderID       FlexString `json:"order_id"`
	CurrentStatus string     `json:"current_status"`
	PickupDate    string     `json:"pickup_date"`
	DeliveredDate string     `json:"delivered_date"`
	DeliveredTo   string     `json:"delivered_to"`
	Destination   string     `json:"destination"`
	Origin        string     `json:"origin"`
	EDD           string     `json:"edd"`
}

// TrackingData is the body of "tracking_data" in a tracking response.
type TrackingData struct {
	TrackStatus    int             `json:"track_status"`
	ShipmentStatus FlexString      `json:"shipment_status"`
	ShipmentTrack  []ShipmentTrack `json:"shipment_track"`
	Activities     []TrackActivity `json:"shipment_track_activities"`
	TrackURL       string          `json:"track_url"`
	ETD            string          `json:"etd"`
	Error          string          `json:"error"`
}

// ServiceabilityRequest is the query of GET /courier/serviceability/.
type ServiceabilityRequest struct {
	PickupPostcode   string
	DeliveryPostcode string
	WeightKg         float64
	COD              bool
	DeclaredValue    float64
	LengthCm         float64
	BreadthCm        float64
	HeightCm         float64
}

// CourierOption is one available courier with its quoted rate.
type CourierOption struct {
	CourierCompanyID      FlexString `json:"courier_company_id"`
	CourierName           string     `json:"courier_name"`
	Rate                  FlexFloat  `json:"rate"`
	FreightCharge         FlexFloat  `json:"freight_charge"`
	CODCharges            FlexFloat  `json:"cod_charges"`
	EstimatedDeliveryDays FlexString `json:"estimated_delivery_days"`
	ETD                   string     `json:"etd"`
	Rating                FlexFloat  `json:"rating"`
	IsSurface             bool       `json:"is_surface"`
}

// ServiceabilityResponse is the parsed serviceability result.
type ServiceabilityResponse struct {
	Couriers             []CourierOption
	RecommendedCourierID string
}

// PickupLocation is a registered pickup address.
type PickupLocation struct {
	ID             FlexString `json:"id"`
	PickupLocation string     `json:"pickup_location"`
	Name           string     `json:"name"`
	Address        string     `json:"address"`
	City           string     `json:"city"`
	State          string     `json:"state"`
	PinCode        FlexString `json:"pin_code"`
	Phone          FlexString `json:"phone"`
	Email          string     `json:"email"`
	Primary        FlexString `json:"primary_location"`
}
