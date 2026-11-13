package carriers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openoms-org/openoms/apps/api-server/internal/integration"
)

// Helpers shared by the Indian carrier adapters (Shiprocket, Delhivery).

// settingFloat is a numeric integration setting. The dashboard's integration
// form submits every text field as a string, so it accepts both a JSON number
// (0.5) and a numeric string ("0.5"); "" and null mean "not set" (0).
type settingFloat float64

// UnmarshalJSON implements json.Unmarshaler.
func (f *settingFloat) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		*f = 0
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s = strings.ReplaceAll(strings.TrimSpace(str), ",", ".")
		if s == "" {
			*f = 0
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return fmt.Errorf("%q is not a valid non-negative number", s)
	}
	*f = settingFloat(v)
	return nil
}

// istLocation is Indian Standard Time (UTC+05:30, no DST).
var istLocation = time.FixedZone("IST", 5*3600+30*60)

// isIndianPIN reports whether s is a 6-digit Indian PIN code (first digit 1-9).
func isIndianPIN(s string) bool {
	if len(s) != 6 || s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// normalizeIndianPIN strips spaces and dashes ("110 001" -> "110001").
func normalizeIndianPIN(s string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(s))
}

// normalizeIndianPhone reduces a phone number to the 10-digit national mobile
// number that Indian carriers expect: it drops every non-digit and then a
// leading country code (91 / 0091) or trunk prefix (0).
// ok is false when the result is not a plausible 10-digit number.
func normalizeIndianPhone(s string) (phone string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	digits := b.String()
	switch {
	case len(digits) == 14 && strings.HasPrefix(digits, "0091"):
		digits = digits[4:]
	case len(digits) == 12 && strings.HasPrefix(digits, "91"):
		digits = digits[2:]
	case len(digits) == 11 && strings.HasPrefix(digits, "0"):
		digits = digits[1:]
	}
	if len(digits) != 10 || digits[0] == '0' {
		return digits, false
	}
	return digits, true
}

// indiaPINPrefix3 holds 3-digit PIN prefixes whose state differs from the
// 2-digit default in indiaPINPrefix2.
var indiaPINPrefix3 = map[string]string{
	"160": "Chandigarh",
	"194": "Ladakh",
	"246": "Uttarakhand", "248": "Uttarakhand", "249": "Uttarakhand", "263": "Uttarakhand",
	"403": "Goa",
	"605": "Puducherry",
	"682": "Kerala",
	"737": "Sikkim",
	"744": "Andaman and Nicobar Islands",
	"790": "Arunachal Pradesh", "791": "Arunachal Pradesh", "792": "Arunachal Pradesh",
	"793": "Meghalaya", "794": "Meghalaya",
	"795": "Manipur",
	"796": "Mizoram",
	"797": "Nagaland", "798": "Nagaland",
	"799": "Tripura",
	"814": "Jharkhand", "815": "Jharkhand", "816": "Jharkhand", "822": "Jharkhand",
	"825": "Jharkhand", "826": "Jharkhand", "827": "Jharkhand", "828": "Jharkhand", "829": "Jharkhand",
	"831": "Jharkhand", "832": "Jharkhand", "833": "Jharkhand", "834": "Jharkhand", "835": "Jharkhand",
}

// indiaPINPrefix2 maps the first two PIN digits to the state that covers
// (almost) the whole range.
var indiaPINPrefix2 = map[string]string{
	"11": "Delhi",
	"12": "Haryana", "13": "Haryana",
	"14": "Punjab", "15": "Punjab", "16": "Punjab",
	"17": "Himachal Pradesh",
	"18": "Jammu and Kashmir", "19": "Jammu and Kashmir",
	"20": "Uttar Pradesh", "21": "Uttar Pradesh", "22": "Uttar Pradesh", "23": "Uttar Pradesh", "24": "Uttar Pradesh",
	"25": "Uttar Pradesh", "26": "Uttar Pradesh", "27": "Uttar Pradesh", "28": "Uttar Pradesh",
	"30": "Rajasthan", "31": "Rajasthan", "32": "Rajasthan", "33": "Rajasthan", "34": "Rajasthan",
	"36": "Gujarat", "37": "Gujarat", "38": "Gujarat", "39": "Gujarat",
	"40": "Maharashtra", "41": "Maharashtra", "42": "Maharashtra", "43": "Maharashtra", "44": "Maharashtra",
	"45": "Madhya Pradesh", "46": "Madhya Pradesh", "47": "Madhya Pradesh", "48": "Madhya Pradesh",
	"49": "Chhattisgarh",
	"50": "Telangana",
	"51": "Andhra Pradesh", "52": "Andhra Pradesh", "53": "Andhra Pradesh",
	"56": "Karnataka", "57": "Karnataka", "58": "Karnataka", "59": "Karnataka",
	"60": "Tamil Nadu", "61": "Tamil Nadu", "62": "Tamil Nadu", "63": "Tamil Nadu", "64": "Tamil Nadu",
	"67": "Kerala", "68": "Kerala", "69": "Kerala",
	"70": "West Bengal", "71": "West Bengal", "72": "West Bengal", "73": "West Bengal", "74": "West Bengal",
	"75": "Odisha", "76": "Odisha", "77": "Odisha",
	"78": "Assam",
	"80": "Bihar", "81": "Bihar", "82": "Bihar", "83": "Bihar", "84": "Bihar", "85": "Bihar",
}

// indiaStateFromPIN returns the state a PIN code most likely belongs to, or
// "" when it cannot be determined.
//
// This is a FALLBACK for orders whose address has no state. It is derived
// from the postal-circle prefix and is approximate near a few borders where a
// 3-digit range is shared (e.g. Uttar Pradesh / Uttarakhand, Chandigarh /
// Mohali, Daman & Diu inside Gujarat ranges). Couriers route by PIN, so a
// wrong state never misroutes a parcel, but it is printed on the label: an
// explicit state on the address always wins.
func indiaStateFromPIN(pin string) string {
	if !isIndianPIN(pin) {
		return ""
	}
	if state, ok := indiaPINPrefix3[pin[:3]]; ok {
		return state
	}
	return indiaPINPrefix2[pin[:2]]
}

// indiaReceiver is a validated, normalised recipient for an Indian carrier.
type indiaReceiver struct {
	Name  string
	Phone string
	PIN   string
	State string
}

// validateIndiaReceiver checks the fields every Indian carrier rejects a
// booking for, returning actionable errors before any API call is made.
func validateIndiaReceiver(provider string, r integration.CarrierReceiver) (indiaReceiver, error) {
	out := indiaReceiver{Name: strings.TrimSpace(r.Name)}
	if out.Name == "" {
		return out, fmt.Errorf("%s: recipient name is required", provider)
	}
	if strings.TrimSpace(r.Street) == "" {
		return out, fmt.Errorf("%s: recipient address is required", provider)
	}
	if c := strings.ToUpper(strings.TrimSpace(r.Country)); c != "" && c != "IN" && c != "IND" && c != "INDIA" {
		return out, fmt.Errorf("%s: only domestic shipments within India are supported (recipient country is %q)", provider, r.Country)
	}
	out.PIN = normalizeIndianPIN(r.PostalCode)
	if !isIndianPIN(out.PIN) {
		return out, fmt.Errorf("%s: recipient PIN code %q is not a valid 6-digit PIN", provider, r.PostalCode)
	}
	phone, ok := normalizeIndianPhone(r.Phone)
	if !ok {
		return out, fmt.Errorf("%s: recipient phone %q is not a valid 10-digit Indian mobile number", provider, r.Phone)
	}
	out.Phone = phone
	out.State = strings.TrimSpace(r.State)
	if out.State == "" {
		out.State = indiaStateFromPIN(out.PIN)
	}
	return out, nil
}

// carrierItemsValue returns the total value and unit count of the items.
func carrierItemsValue(items []integration.CarrierItem) (total float64, units int) {
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		total += it.UnitPrice * float64(qty)
		units += qty
	}
	return total, units
}

// shipmentValue picks the value to declare for a shipment: the order total,
// else the sum of the items, else the COD amount, else the insured value.
func shipmentValue(req integration.CarrierShipmentRequest) float64 {
	if req.DeclaredValue > 0 {
		return req.DeclaredValue
	}
	if total, _ := carrierItemsValue(req.Items); total > 0 {
		return total
	}
	if req.CODAmount > 0 {
		return req.CODAmount
	}
	return req.InsuredValue
}

// requireINR rejects amounts that are explicitly in a non-INR currency: the
// Indian carriers take rupee amounts and silently relabelling e.g. PLN as INR
// would collect the wrong COD amount.
func requireINR(provider, currency string) error {
	if c := strings.ToUpper(strings.TrimSpace(currency)); c != "" && c != "INR" {
		return fmt.Errorf("%s: amounts must be in INR, but the order currency is %s", provider, c)
	}
	return nil
}

// truncateRunes shortens s to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
