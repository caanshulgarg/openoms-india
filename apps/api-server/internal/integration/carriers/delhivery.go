package carriers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	delhivery "github.com/openoms-org/openoms/packages/delhivery-go-sdk"

	"github.com/openoms-org/openoms/apps/api-server/internal/integration"
	"github.com/openoms-org/openoms/apps/api-server/internal/netutil"
)

func init() {
	integration.RegisterCarrierProvider("delhivery", func(credentials json.RawMessage, settings json.RawMessage) (integration.CarrierProvider, error) {
		return NewDelhiveryProvider(credentials, settings)
	})
}

// DelhiveryCredentials is the JSON structure stored in encrypted integration
// credentials. Staging and production use different tokens.
type DelhiveryCredentials struct {
	APIToken string `json:"api_token"`
	Sandbox  bool   `json:"sandbox,omitempty"`
}

// delhiverySettings holds the provider settings.
type delhiverySettings struct {
	// PickupLocation is the registered warehouse name in the Delhivery
	// account (exact, case-sensitive). Required to create shipments.
	PickupLocation string `json:"pickup_location"`
	// ShippingMode is the default mode: "Surface" (default) or "Express".
	ShippingMode string `json:"shipping_mode,omitempty"`
	// SellerName / SellerGSTIN / DefaultHSNCode are printed on the label and
	// invoice section of the packing slip.
	SellerName     string `json:"seller_name,omitempty"`
	SellerGSTIN    string `json:"seller_gst_tin,omitempty"`
	DefaultHSNCode string `json:"default_hsn_code,omitempty"`
	// LabelSize is "A4" or "4R" (4x6 thermal). Empty uses the account default.
	LabelSize string `json:"label_size,omitempty"`
	// DefaultWeightKg is used when the label request carries no weight.
	DefaultWeightKg settingFloat `json:"default_weight_kg,omitempty"`
}

// delhiveryClient is the subset of the SDK used by the provider.
type delhiveryClient interface {
	CreateShipment(ctx context.Context, req *delhivery.CreateRequest) (*delhivery.CreateResponse, error)
	PackingSlip(ctx context.Context, waybill, pdfSize string) (*delhivery.PackingSlip, error)
	Download(ctx context.Context, fileURL string) ([]byte, error)
	Track(ctx context.Context, waybills ...string) ([]delhivery.TrackedShipment, error)
	Cancel(ctx context.Context, waybill string) error
	ShippingCharges(ctx context.Context, req delhivery.ChargesRequest) ([]delhivery.Charge, error)
}

// DelhiveryProvider implements integration.CarrierProvider for Delhivery
// Express (B2C parcels, India).
type DelhiveryProvider struct {
	client   delhiveryClient
	settings delhiverySettings
	logger   *slog.Logger
	now      func() time.Time
}

// NewDelhiveryProvider creates a Delhivery CarrierProvider from encrypted
// credentials and settings.
func NewDelhiveryProvider(credentials json.RawMessage, settings json.RawMessage) (*DelhiveryProvider, error) {
	var creds DelhiveryCredentials
	if err := json.Unmarshal(credentials, &creds); err != nil {
		return nil, fmt.Errorf("delhivery: parse credentials: %w", err)
	}
	creds.APIToken = strings.TrimSpace(creds.APIToken)
	if creds.APIToken == "" {
		return nil, fmt.Errorf("delhivery: credentials must include api_token")
	}

	cfg, err := parseDelhiverySettings(settings)
	if err != nil {
		return nil, err
	}

	opts := []delhivery.Option{delhivery.WithHTTPClient(netutil.SafeHTTPClient(30 * time.Second))}
	if creds.Sandbox {
		opts = append(opts, delhivery.WithStaging())
	}

	return &DelhiveryProvider{
		client:   delhivery.NewClient(creds.APIToken, opts...),
		settings: cfg,
		logger:   slog.Default().With("provider", "delhivery"),
		now:      time.Now,
	}, nil
}

func parseDelhiverySettings(settings json.RawMessage) (delhiverySettings, error) {
	var cfg delhiverySettings
	if len(settings) > 0 && string(settings) != "null" {
		if err := json.Unmarshal(settings, &cfg); err != nil {
			return cfg, fmt.Errorf("delhivery: parse settings: %w", err)
		}
	}
	cfg.PickupLocation = strings.TrimSpace(cfg.PickupLocation)
	switch strings.ToLower(strings.TrimSpace(cfg.ShippingMode)) {
	case "", "surface":
		cfg.ShippingMode = delhivery.ModeSurface
	case "express":
		cfg.ShippingMode = delhivery.ModeExpress
	default:
		return cfg, fmt.Errorf("delhivery: shipping_mode must be \"Surface\" or \"Express\", got %q", cfg.ShippingMode)
	}
	switch strings.ToUpper(strings.TrimSpace(cfg.LabelSize)) {
	case "":
		cfg.LabelSize = ""
	case "A4":
		cfg.LabelSize = "A4"
	case "4R":
		cfg.LabelSize = "4R"
	default:
		return cfg, fmt.Errorf("delhivery: label_size must be \"A4\" or \"4R\", got %q", cfg.LabelSize)
	}
	return cfg, nil
}

// ProviderName returns the carrier provider identifier.
func (p *DelhiveryProvider) ProviderName() string { return "delhivery" }

// resolveDelhiveryMode maps the label request's service type to a Delhivery
// shipping mode. "" and "standard" use the configured default.
func (p *DelhiveryProvider) resolveDelhiveryMode(serviceType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "", "standard", "auto":
		return p.settings.ShippingMode, nil
	case "surface":
		return delhivery.ModeSurface, nil
	case "express", "air":
		return delhivery.ModeExpress, nil
	default:
		return "", fmt.Errorf("delhivery: unknown service type %q — valid types: surface, express", serviceType)
	}
}

// CreateShipment manifests a forward shipment; Delhivery assigns the waybill.
func (p *DelhiveryProvider) CreateShipment(ctx context.Context, req integration.CarrierShipmentRequest) (*integration.CarrierShipmentResponse, error) {
	if p.settings.PickupLocation == "" {
		return nil, fmt.Errorf("delhivery: pickup location is not configured — set the exact warehouse name from your Delhivery account in the integration settings")
	}
	mode, err := p.resolveDelhiveryMode(req.ServiceType)
	if err != nil {
		return nil, err
	}
	if err := requireINR("delhivery", req.Currency); err != nil {
		return nil, err
	}
	if c := strings.ToUpper(strings.TrimSpace(req.CODCurrency)); c != "" && c != "INR" {
		return nil, fmt.Errorf("delhivery: COD must be in INR, got %s", c)
	}
	receiver, err := validateIndiaReceiver("delhivery", req.Receiver)
	if err != nil {
		return nil, err
	}

	weightKg := req.Parcel.WeightKg
	if weightKg <= 0 {
		weightKg = float64(p.settings.DefaultWeightKg)
	}
	if weightKg <= 0 {
		return nil, fmt.Errorf("delhivery: parcel weight is required (enter it on the label, or set a default weight in the integration settings)")
	}

	orderRef := strings.TrimSpace(req.Reference)
	if orderRef == "" {
		orderRef = strings.TrimSpace(req.OrderID)
	}
	if orderRef == "" {
		return nil, fmt.Errorf("delhivery: order reference is required")
	}

	value := shipmentValue(req)
	paymentMode := delhivery.PaymentPrepaid
	codAmount := 0.0
	if req.CODAmount > 0 {
		paymentMode = delhivery.PaymentCOD
		codAmount = req.CODAmount
		if value < codAmount {
			value = codAmount
		}
	}

	desc, units := delhiveryProductsDesc(req.Items)
	if desc == "" {
		desc = "Order " + truncateRunes(orderRef, 40)
	}
	if units <= 0 {
		units = 1
	}

	shipment := delhivery.Shipment{
		Name:         receiver.Name,
		Add:          strings.TrimSpace(req.Receiver.Street),
		Pin:          receiver.PIN,
		City:         strings.TrimSpace(req.Receiver.City),
		State:        receiver.State,
		Country:      "India",
		Phone:        receiver.Phone,
		Order:        orderRef,
		PaymentMode:  paymentMode,
		CODAmount:    codAmount,
		TotalAmount:  value,
		ProductsDesc: desc,
		HSNCode:      p.settings.DefaultHSNCode,
		Quantity:     units,
		OrderDate:    p.now().In(istLocation).Format("2006-01-02 15:04:05"),
		Weight:       math.Round(weightKg * 1000), // kg -> grams
		Width:        req.Parcel.WidthCm,
		Height:       req.Parcel.HeightCm,
		Length:       req.Parcel.DepthCm,
		ShippingMode: mode,
		SellerName:   p.settings.SellerName,
		SellerGSTTIN: p.settings.SellerGSTIN,
	}

	resp, err := p.client.CreateShipment(ctx, &delhivery.CreateRequest{
		Shipments:      []delhivery.Shipment{shipment},
		PickupLocation: delhivery.PickupLocationRef{Name: p.settings.PickupLocation},
	})
	if err != nil {
		return nil, fmt.Errorf("delhivery: create shipment: %w", err)
	}
	if len(resp.Packages) == 0 || resp.Packages[0].Waybill == "" {
		return nil, fmt.Errorf("delhivery: create shipment: no waybill returned")
	}
	waybill := resp.Packages[0].Waybill.String()

	return &integration.CarrierShipmentResponse{
		ExternalID:     waybill,
		TrackingNumber: waybill,
		Status:         delhivery.StatusKey("UD", "Manifested"),
	}, nil
}

// delhiveryProductsDesc renders the item list as "Name x2, Other x1" (capped
// at 250 characters) and returns the total unit count.
func delhiveryProductsDesc(items []integration.CarrierItem) (desc string, units int) {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		name := strings.TrimSpace(it.Name)
		if name == "" {
			name = strings.TrimSpace(it.SKU)
		}
		if name == "" {
			continue
		}
		units += qty
		parts = append(parts, fmt.Sprintf("%s x%d", name, qty))
	}
	return truncateRunes(strings.Join(parts, ", "), 250), units
}

// GetLabel fetches the packing slip (shipping label) PDF for a waybill.
func (p *DelhiveryProvider) GetLabel(ctx context.Context, externalID string, format string) ([]byte, error) {
	if f := strings.ToLower(strings.TrimSpace(format)); f != "" && f != "pdf" {
		return nil, fmt.Errorf("delhivery: label format %q is not supported — Delhivery labels are PDF only", format)
	}
	slip, err := p.client.PackingSlip(ctx, externalID, p.settings.LabelSize)
	if err != nil {
		return nil, fmt.Errorf("delhivery: packing slip: %w", err)
	}
	data, err := p.client.Download(ctx, slip.PDFDownloadLink)
	if err != nil {
		return nil, fmt.Errorf("delhivery: download label: %w", err)
	}
	return data, nil
}

// GetTracking returns tracking events for a waybill in chronological order
// (oldest first; the last event is the current state). Event statuses are
// "<StatusType>:<Status>" keys, e.g. "UD:In Transit" (see MapStatus).
func (p *DelhiveryProvider) GetTracking(ctx context.Context, trackingNumber string) ([]integration.TrackingEvent, error) {
	shipments, err := p.client.Track(ctx, trackingNumber)
	if err != nil {
		return nil, fmt.Errorf("delhivery: get tracking: %w", err)
	}
	if len(shipments) == 0 {
		return nil, nil
	}
	s := shipments[0]

	events := make([]integration.TrackingEvent, 0, len(s.Scans)+1)
	for _, scan := range s.Scans {
		d := scan.ScanDetail
		if strings.TrimSpace(d.Scan) == "" {
			continue
		}
		ts := delhivery.ParseTime(d.ScanDateTime)
		if ts.IsZero() {
			ts = delhivery.ParseTime(d.StatusDateTime)
		}
		events = append(events, integration.TrackingEvent{
			Status:    delhivery.StatusKey(d.ScanType, d.Scan),
			Location:  strings.TrimSpace(d.ScannedLocation),
			Timestamp: ts,
			Details:   strings.TrimSpace(d.Instructions),
		})
	}

	// The summary Status block is authoritative for the current state. Make
	// sure it is the last event even when the scan list lags behind it.
	if strings.TrimSpace(s.Status.Status) != "" {
		current := delhivery.StatusKey(s.Status.StatusType, s.Status.Status)
		if len(events) == 0 || events[len(events)-1].Status != current {
			ts := delhivery.ParseTime(s.Status.StatusDateTime)
			if ts.IsZero() {
				ts = p.now()
			}
			events = append(events, integration.TrackingEvent{
				Status:    current,
				Location:  strings.TrimSpace(s.Status.StatusLocation),
				Timestamp: ts,
				Details:   strings.TrimSpace(s.Status.Instructions),
			})
		}
	}
	return events, nil
}

// CancelShipment cancels a Delhivery shipment by waybill.
func (p *DelhiveryProvider) CancelShipment(ctx context.Context, externalID string) error {
	if err := p.client.Cancel(ctx, externalID); err != nil {
		return fmt.Errorf("delhivery: cancel shipment: %w", err)
	}
	return nil
}

// MapStatus maps a Delhivery "<StatusType>:<Status>" key to the internal OMS
// shipment status.
func (p *DelhiveryProvider) MapStatus(carrierStatus string) (string, bool) {
	return delhivery.MapStatusKey(carrierStatus)
}

// GetRates returns Delhivery's estimated charge for Surface and Express on
// the lane. These are estimates: the billed amount depends on the final
// charged weight and zone.
func (p *DelhiveryProvider) GetRates(ctx context.Context, req integration.RateRequest) ([]integration.Rate, error) {
	from := normalizeIndianPIN(req.FromPostalCode)
	to := normalizeIndianPIN(req.ToPostalCode)
	if !isIndianPIN(from) || !isIndianPIN(to) {
		// Not an Indian lane: Delhivery has nothing to quote.
		return nil, nil
	}
	weightKg := req.Weight
	if weightKg <= 0 {
		weightKg = firstPositive(float64(p.settings.DefaultWeightKg), 0.5)
	}
	// Chargeable weight is the greater of dead weight and volumetric weight
	// (L x W x H / 5000, in kg).
	if req.Length > 0 && req.Width > 0 && req.Height > 0 {
		if vol := req.Length * req.Width * req.Height / 5000; vol > weightKg {
			weightKg = vol
		}
	}
	grams := int(math.Ceil(weightKg * 1000))

	modes := []struct {
		mode    string
		service string
		label   string
	}{
		{delhivery.ModeSurface, "surface", "Delhivery Surface"},
		{delhivery.ModeExpress, "express", "Delhivery Express"},
	}

	rates := make([]integration.Rate, 0, len(modes))
	var lastErr error
	for _, m := range modes {
		charges, err := p.client.ShippingCharges(ctx, delhivery.ChargesRequest{
			Mode:           m.mode,
			OriginPin:      from,
			DestinationPin: to,
			WeightGrams:    grams,
			COD:            req.COD > 0,
			CODAmount:      req.COD,
		})
		if err != nil {
			lastErr = err
			p.logger.Warn("delhivery: shipping charge lookup failed", "mode", m.mode, "error", err)
			continue
		}
		if len(charges) == 0 || charges[0].TotalAmount <= 0 {
			continue
		}
		rates = append(rates, integration.Rate{
			CarrierName: m.label,
			CarrierCode: "delhivery",
			ServiceName: m.service,
			Price:       float64(charges[0].TotalAmount),
			Currency:    "INR",
			PickupPoint: false,
			IsEstimate:  true,
		})
	}
	if len(rates) == 0 && lastErr != nil {
		return nil, fmt.Errorf("delhivery: shipping charges: %w", lastErr)
	}
	return rates, nil
}

// SupportsPickupPoints reports that Delhivery delivers door-to-door only.
func (p *DelhiveryProvider) SupportsPickupPoints() bool { return false }

// SearchPickupPoints is not applicable to Delhivery.
func (p *DelhiveryProvider) SearchPickupPoints(_ context.Context, _ string) ([]integration.PickupPoint, error) {
	return nil, fmt.Errorf("delhivery: pickup points are not supported")
}
