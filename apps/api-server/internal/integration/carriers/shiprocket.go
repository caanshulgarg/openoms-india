package carriers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	shiprocket "github.com/openoms-org/openoms/packages/shiprocket-go-sdk"

	"github.com/openoms-org/openoms/apps/api-server/internal/integration"
	"github.com/openoms-org/openoms/apps/api-server/internal/netutil"
)

func init() {
	integration.RegisterCarrierProvider("shiprocket", func(credentials json.RawMessage, settings json.RawMessage) (integration.CarrierProvider, error) {
		return NewShiprocketProvider(credentials, settings)
	})
}

// ShiprocketCredentials is the JSON structure stored in encrypted integration
// credentials. They are the API user's login (Shiprocket panel -> Settings ->
// API -> Configure -> Create an API User), not the main account login.
type ShiprocketCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// shiprocketSettings holds the provider settings.
type shiprocketSettings struct {
	// PickupLocation is the nickname of a pickup address registered in the
	// Shiprocket account. Required to create shipments.
	PickupLocation string `json:"pickup_location"`
	// DefaultCourierID pins a courier when the label request does not name
	// one. Empty lets Shiprocket choose from the account's courier rules.
	DefaultCourierID string `json:"default_courier_id,omitempty"`
	// ManualPickup disables the automatic pickup request. By default a
	// courier pickup is scheduled right after each AWB assignment; sellers
	// who schedule pickups themselves in the Shiprocket panel set this.
	ManualPickup bool `json:"manual_pickup,omitempty"`
	// Defaults applied when the label request carries no weight / dimensions.
	DefaultWeightKg settingFloat `json:"default_weight_kg,omitempty"`
	DefaultLengthCm settingFloat `json:"default_length_cm,omitempty"`
	DefaultWidthCm  settingFloat `json:"default_width_cm,omitempty"`
	DefaultHeightCm settingFloat `json:"default_height_cm,omitempty"`
	// ChannelID attaches orders to a specific Shiprocket channel (optional).
	ChannelID string `json:"channel_id,omitempty"`
}

// shiprocketClient is the subset of the SDK used by the provider.
type shiprocketClient interface {
	CreateOrder(ctx context.Context, req *shiprocket.CreateOrderRequest) (*shiprocket.CreateOrderResponse, error)
	AssignAWB(ctx context.Context, shipmentID, courierID string) (*shiprocket.AWBAssignment, error)
	GeneratePickup(ctx context.Context, shipmentID string) (*shiprocket.PickupResult, error)
	GenerateLabel(ctx context.Context, shipmentIDs ...string) (string, error)
	Download(ctx context.Context, fileURL string) ([]byte, error)
	TrackByAWB(ctx context.Context, awb string) (*shiprocket.TrackingData, error)
	CancelOrders(ctx context.Context, orderIDs ...string) error
	Serviceability(ctx context.Context, req shiprocket.ServiceabilityRequest) (*shiprocket.ServiceabilityResponse, error)
}

// ShiprocketProvider implements integration.CarrierProvider for the Shiprocket
// multi-courier aggregator (India).
type ShiprocketProvider struct {
	client   shiprocketClient
	settings shiprocketSettings
	logger   *slog.Logger
	now      func() time.Time
}

// NewShiprocketProvider creates a Shiprocket CarrierProvider from encrypted
// credentials and settings.
func NewShiprocketProvider(credentials json.RawMessage, settings json.RawMessage) (*ShiprocketProvider, error) {
	var creds ShiprocketCredentials
	if err := json.Unmarshal(credentials, &creds); err != nil {
		return nil, fmt.Errorf("shiprocket: parse credentials: %w", err)
	}
	creds.Email = strings.TrimSpace(creds.Email)
	if creds.Email == "" || creds.Password == "" {
		return nil, fmt.Errorf("shiprocket: credentials must include the API user's email and password")
	}

	cfg, err := parseShiprocketSettings(settings)
	if err != nil {
		return nil, err
	}

	client := shiprocket.NewClient(creds.Email, creds.Password,
		shiprocket.WithHTTPClient(netutil.SafeHTTPClient(30*time.Second)))

	return &ShiprocketProvider{
		client:   client,
		settings: cfg,
		logger:   slog.Default().With("provider", "shiprocket"),
		now:      time.Now,
	}, nil
}

func parseShiprocketSettings(settings json.RawMessage) (shiprocketSettings, error) {
	var cfg shiprocketSettings
	if len(settings) > 0 && string(settings) != "null" {
		if err := json.Unmarshal(settings, &cfg); err != nil {
			return cfg, fmt.Errorf("shiprocket: parse settings: %w", err)
		}
	}
	cfg.PickupLocation = strings.TrimSpace(cfg.PickupLocation)
	cfg.DefaultCourierID = strings.TrimSpace(cfg.DefaultCourierID)
	return cfg, nil
}

// ProviderName returns the carrier provider identifier.
func (p *ShiprocketProvider) ProviderName() string { return "shiprocket" }

// shiprocketExternalID packs the two ids Shiprocket needs later into the one
// external id OpenOMS stores: labels are requested by shipment id, cancels by
// order id.
func shiprocketExternalID(orderID, shipmentID string) string {
	return orderID + ":" + shipmentID
}

// parseShiprocketExternalID is the inverse of shiprocketExternalID. A bare id
// (no separator) is treated as a shipment id with an unknown order id.
func parseShiprocketExternalID(externalID string) (orderID, shipmentID string) {
	externalID = strings.TrimSpace(externalID)
	if before, after, found := strings.Cut(externalID, ":"); found {
		return before, after
	}
	return "", externalID
}

// resolveShiprocketCourier turns the label request's service type into a
// courier id. "", "standard" and "auto" mean "let Shiprocket choose" (or the
// configured default courier); anything else must be a numeric courier id, as
// returned in Rate.ServiceName by GetRates.
func (p *ShiprocketProvider) resolveShiprocketCourier(serviceType string) (string, error) {
	st := strings.ToLower(strings.TrimSpace(serviceType))
	switch st {
	case "", "standard", "auto":
		return p.settings.DefaultCourierID, nil
	}
	if _, err := strconv.ParseUint(st, 10, 64); err != nil {
		return "", fmt.Errorf("shiprocket: unknown service type %q — use \"auto\" or a numeric Shiprocket courier id", serviceType)
	}
	return st, nil
}

// CreateShipment creates a Shiprocket order, assigns a courier + AWB and
// (unless disabled) schedules the pickup.
func (p *ShiprocketProvider) CreateShipment(ctx context.Context, req integration.CarrierShipmentRequest) (*integration.CarrierShipmentResponse, error) {
	if p.settings.PickupLocation == "" {
		return nil, fmt.Errorf("shiprocket: pickup location is not configured — set the pickup location nickname from your Shiprocket account in the integration settings")
	}
	courierID, err := p.resolveShiprocketCourier(req.ServiceType)
	if err != nil {
		return nil, err
	}
	if err := requireINR("shiprocket", req.Currency); err != nil {
		return nil, err
	}
	if c := strings.ToUpper(strings.TrimSpace(req.CODCurrency)); c != "" && c != "INR" {
		return nil, fmt.Errorf("shiprocket: COD must be in INR, got %s", c)
	}
	receiver, err := validateIndiaReceiver("shiprocket", req.Receiver)
	if err != nil {
		return nil, err
	}
	if receiver.State == "" {
		return nil, fmt.Errorf("shiprocket: recipient state is required and could not be derived from PIN %s", receiver.PIN)
	}

	weight := req.Parcel.WeightKg
	if weight <= 0 {
		weight = float64(p.settings.DefaultWeightKg)
	}
	if weight <= 0 {
		return nil, fmt.Errorf("shiprocket: parcel weight is required (enter it on the label, or set a default weight in the integration settings)")
	}
	length := firstPositive(req.Parcel.DepthCm, float64(p.settings.DefaultLengthCm), 10)
	breadth := firstPositive(req.Parcel.WidthCm, float64(p.settings.DefaultWidthCm), 10)
	height := firstPositive(req.Parcel.HeightCm, float64(p.settings.DefaultHeightCm), 10)

	orderRef := strings.TrimSpace(req.Reference)
	if orderRef == "" {
		orderRef = strings.TrimSpace(req.OrderID)
	}
	if orderRef == "" {
		return nil, fmt.Errorf("shiprocket: order reference is required")
	}

	value := shipmentValue(req)
	paymentMethod := "Prepaid"
	subTotal := value
	if req.CODAmount > 0 {
		// For COD, sub_total is the amount the courier collects.
		paymentMethod = "COD"
		subTotal = req.CODAmount
	}
	if subTotal <= 0 {
		return nil, fmt.Errorf("shiprocket: order value is required (the order has no total amount, item prices or COD amount)")
	}

	firstName, lastName := splitPersonName(receiver.Name)
	srReq := &shiprocket.CreateOrderRequest{
		OrderID:             truncateRunes(orderRef, 50),
		OrderDate:           p.now().In(istLocation).Format("2006-01-02 15:04"),
		PickupLocation:      p.settings.PickupLocation,
		ChannelID:           p.settings.ChannelID,
		BillingCustomerName: firstName,
		BillingLastName:     lastName,
		BillingAddress:      strings.TrimSpace(req.Receiver.Street),
		BillingCity:         strings.TrimSpace(req.Receiver.City),
		BillingPincode:      receiver.PIN,
		BillingState:        receiver.State,
		BillingCountry:      "India",
		BillingEmail:        strings.TrimSpace(req.Receiver.Email),
		BillingPhone:        receiver.Phone,
		ShippingIsBilling:   true,
		OrderItems:          shiprocketItems(req, orderRef, subTotal),
		PaymentMethod:       paymentMethod,
		SubTotal:            subTotal,
		Length:              length,
		Breadth:             breadth,
		Height:              height,
		Weight:              weight,
	}

	order, err := p.client.CreateOrder(ctx, srReq)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: create order: %w", err)
	}
	srOrderID, srShipmentID := order.OrderID.String(), order.ShipmentID.String()

	awb, err := p.client.AssignAWB(ctx, srShipmentID, courierID)
	if err != nil {
		// The Shiprocket order now exists without an AWB. It is left in place:
		// retrying the label reuses it (same order_id), and it stays visible in
		// the Shiprocket panel for manual handling.
		return nil, fmt.Errorf("shiprocket: order %s was created but no courier could be assigned (shipment %s): %w", srOrderID, srShipmentID, err)
	}

	if !p.settings.ManualPickup {
		if _, err := p.client.GeneratePickup(ctx, srShipmentID); err != nil {
			// Not fatal: the AWB and label are valid; the pickup can be
			// requested again from the Shiprocket panel.
			p.logger.Warn("shiprocket: pickup scheduling failed — schedule it from the Shiprocket panel",
				"shipment_id", srShipmentID, "awb", awb.AWBCode, "error", err)
		}
	}

	return &integration.CarrierShipmentResponse{
		ExternalID:     shiprocketExternalID(srOrderID, srShipmentID),
		TrackingNumber: awb.AWBCode,
		Status:         "AWB ASSIGNED",
	}, nil
}

// shiprocketItems builds the order lines. Without usable items a single
// summary line carrying the whole value is sent (Shiprocket requires at least
// one line with a name, SKU, units and price).
func shiprocketItems(req integration.CarrierShipmentRequest, orderRef string, subTotal float64) []shiprocket.OrderItem {
	items := make([]shiprocket.OrderItem, 0, len(req.Items))
	for i, it := range req.Items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		name := strings.TrimSpace(it.Name)
		sku := strings.TrimSpace(it.SKU)
		if name == "" {
			name = sku
		}
		if sku == "" {
			sku = "ITEM-" + strconv.Itoa(i+1)
		}
		if name == "" {
			continue
		}
		items = append(items, shiprocket.OrderItem{
			Name:         truncateRunes(name, 200),
			SKU:          truncateRunes(sku, 50),
			Units:        qty,
			SellingPrice: it.UnitPrice,
		})
	}
	if len(items) > 0 {
		return items
	}
	return []shiprocket.OrderItem{{
		Name:         "Order " + truncateRunes(orderRef, 40),
		SKU:          truncateRunes(orderRef, 50),
		Units:        1,
		SellingPrice: subTotal,
	}}
}

// GetLabel generates the label for the shipment and downloads the PDF.
func (p *ShiprocketProvider) GetLabel(ctx context.Context, externalID string, format string) ([]byte, error) {
	if f := strings.ToLower(strings.TrimSpace(format)); f != "" && f != "pdf" {
		return nil, fmt.Errorf("shiprocket: label format %q is not supported — Shiprocket labels are PDF only", format)
	}
	_, shipmentID := parseShiprocketExternalID(externalID)
	if shipmentID == "" {
		return nil, fmt.Errorf("shiprocket: get label: missing shipment id")
	}
	labelURL, err := p.client.GenerateLabel(ctx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: generate label: %w", err)
	}
	data, err := p.client.Download(ctx, labelURL)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: download label: %w", err)
	}
	return data, nil
}

// GetTracking returns tracking events for an AWB in chronological order
// (oldest first; the last event is the current state).
func (p *ShiprocketProvider) GetTracking(ctx context.Context, trackingNumber string) ([]integration.TrackingEvent, error) {
	data, err := p.client.TrackByAWB(ctx, trackingNumber)
	if err != nil {
		return nil, fmt.Errorf("shiprocket: get tracking: %w", err)
	}

	events := make([]integration.TrackingEvent, 0, len(data.Activities))
	// Shiprocket lists activities newest first.
	for i := len(data.Activities) - 1; i >= 0; i-- {
		act := data.Activities[i]
		status := strings.TrimSpace(act.SRStatusLabel)
		if status == "" || strings.EqualFold(status, "NA") {
			status = strings.TrimSpace(act.Activity)
		}
		if status == "" {
			continue
		}
		events = append(events, integration.TrackingEvent{
			Status:    status,
			Location:  strings.TrimSpace(act.Location),
			Timestamp: parseShiprocketTime(act.Date),
			Details:   strings.TrimSpace(act.Activity),
		})
	}

	// No scans yet (or none carried a status): fall back to the summary state.
	if len(events) == 0 && len(data.ShipmentTrack) > 0 {
		if current := strings.TrimSpace(data.ShipmentTrack[0].CurrentStatus); current != "" {
			events = append(events, integration.TrackingEvent{
				Status:    current,
				Location:  strings.TrimSpace(data.ShipmentTrack[0].Destination),
				Timestamp: p.now(),
			})
		}
	}
	return events, nil
}

// parseShiprocketTime parses "2006-01-02 15:04:05" (IST). Unparseable input
// yields the zero time.
func parseShiprocketTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "02 01 2006 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, istLocation); err == nil {
			return t
		}
	}
	return time.Time{}
}

// CancelShipment cancels the Shiprocket order behind the shipment, which also
// releases its AWB.
func (p *ShiprocketProvider) CancelShipment(ctx context.Context, externalID string) error {
	orderID, _ := parseShiprocketExternalID(externalID)
	if orderID == "" {
		return fmt.Errorf("shiprocket: cancel: external id %q has no Shiprocket order id — cancel the order in the Shiprocket panel", externalID)
	}
	if err := p.client.CancelOrders(ctx, orderID); err != nil {
		return fmt.Errorf("shiprocket: cancel order: %w", err)
	}
	return nil
}

// MapStatus maps a Shiprocket status to the internal OMS shipment status.
func (p *ShiprocketProvider) MapStatus(carrierStatus string) (string, bool) {
	return shiprocket.MapStatus(carrierStatus)
}

// GetRates returns live quotes from every courier that serves the lane.
// Rate.ServiceName carries the courier id, so a chosen rate round-trips as
// CreateShipment's ServiceType.
func (p *ShiprocketProvider) GetRates(ctx context.Context, req integration.RateRequest) ([]integration.Rate, error) {
	from := normalizeIndianPIN(req.FromPostalCode)
	to := normalizeIndianPIN(req.ToPostalCode)
	if !isIndianPIN(from) || !isIndianPIN(to) {
		// Not an Indian lane: Shiprocket has nothing to quote.
		return nil, nil
	}
	weight := req.Weight
	if weight <= 0 {
		weight = firstPositive(float64(p.settings.DefaultWeightKg), 0.5)
	}

	resp, err := p.client.Serviceability(ctx, shiprocket.ServiceabilityRequest{
		PickupPostcode:   from,
		DeliveryPostcode: to,
		WeightKg:         weight,
		COD:              req.COD > 0,
		DeclaredValue:    req.COD,
		LengthCm:         req.Length,
		BreadthCm:        req.Width,
		HeightCm:         req.Height,
	})
	if err != nil {
		return nil, fmt.Errorf("shiprocket: serviceability: %w", err)
	}

	rates := make([]integration.Rate, 0, len(resp.Couriers))
	for _, c := range resp.Couriers {
		if c.CourierCompanyID == "" || c.Rate <= 0 {
			continue
		}
		days, _ := strconv.Atoi(strings.TrimSpace(c.EstimatedDeliveryDays.String()))
		rates = append(rates, integration.Rate{
			CarrierName:   "Shiprocket · " + c.CourierName,
			CarrierCode:   "shiprocket",
			ServiceName:   c.CourierCompanyID.String(),
			Price:         float64(c.Rate),
			Currency:      "INR",
			EstimatedDays: days,
			PickupPoint:   false,
			IsEstimate:    false,
		})
	}
	return rates, nil
}

// SupportsPickupPoints reports that Shiprocket delivers door-to-door only.
func (p *ShiprocketProvider) SupportsPickupPoints() bool { return false }

// SearchPickupPoints is not applicable to Shiprocket.
func (p *ShiprocketProvider) SearchPickupPoints(_ context.Context, _ string) ([]integration.PickupPoint, error) {
	return nil, fmt.Errorf("shiprocket: pickup points are not supported")
}

// firstPositive returns the first value greater than zero, or 0.
func firstPositive(values ...float64) float64 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// splitPersonName splits a full name into first and last name on the last
// space ("Asha Rani Verma" -> "Asha Rani", "Verma").
func splitPersonName(full string) (first, last string) {
	full = strings.Join(strings.Fields(full), " ")
	if i := strings.LastIndex(full, " "); i > 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}
