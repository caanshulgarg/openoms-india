# Shipping in India with Shiprocket and Delhivery

OpenOMS books, labels, tracks and cancels Indian shipments through two carrier
providers:

| Provider | What it is | Use it when |
|---|---|---|
| `shiprocket` | Aggregator: one account, many couriers (Delhivery, Bluedart, Xpressbees, Ecom Express, DTDC, ...) | You want courier choice and live rate comparison per parcel |
| `delhivery` | Direct Delhivery Express (B2C) account | You have your own Delhivery contract and rates |

You can connect both and pick per shipment.

## What works

| Step | Shiprocket | Delhivery |
|---|---|---|
| Create shipment (prepaid and COD) | Creates the order, assigns courier + AWB | Manifests the order, waybill auto-assigned |
| Courier choice | Automatic (account rules), a default courier, or a courier ID per label | Surface or Express |
| Pickup request | Requested automatically after each AWB (can be switched off) | Not requested: see [Pickups](#pickups) |
| Label | PDF | PDF (A4 or 4R / 4x6) |
| Tracking | Polled every 10 minutes, mapped to OMS statuses | Same |
| Cancel | Cancels the Shiprocket order and frees the AWB | Cancels the waybill |
| Rates | Live quote per courier | Estimate for Surface and Express |

## 1. Switch the deployment to India

Add to `.env` (see `.env.example`):

```bash
# Both carriers are "beta" providers. They are hidden unless BOTH are "full".
OPENOMS_API_SURFACE=full
OPENOMS_DASHBOARD_SURFACE=full

# New orders default to rupees and India instead of PLN / PL.
OPENOMS_DEFAULT_CURRENCY=INR
NEXT_PUBLIC_DEFAULT_CURRENCY=INR
NEXT_PUBLIC_DEFAULT_COUNTRY=IN
```

`NEXT_PUBLIC_*` values are baked into the dashboard at build time, so rebuild
the dashboard image after changing them:

```bash
docker compose -f docker-compose.prod.yml up -d --build
```

Notes:

- `full` surface mode also unhides the other controlled/beta features of
  OpenOMS, not only these two carriers.
- **Both carriers refuse an order whose currency is not INR.** This is
  deliberate: an order saved as 999 PLN must not be collected as 999 INR cash
  on delivery. Orders created before the switch keep their old currency.

## 2. Connect Shiprocket

In the Shiprocket panel:

1. **Settings > API > Configure > Create an API User.** Use an email that is
   *not* already a Shiprocket login. Note the email and password: these are the
   credentials OpenOMS needs, not your normal login.
2. **Settings > Pickup Addresses.** Note the *nickname* of the address you ship
   from (for example `Primary`).
3. Make sure the wallet has balance and KYC is complete, otherwise courier
   assignment fails.

In OpenOMS: **Carriers > Add carrier > Shiprocket**.

| Field | Value |
|---|---|
| API user email / password | From step 1 |
| Pickup location nickname | Exactly as in step 2 |
| Default courier ID | Optional. Empty lets Shiprocket choose by your courier rules |
| Default parcel weight | Optional. Used only when a label is generated without a weight |
| "I schedule pickups myself" | Tick to stop OpenOMS requesting a pickup for every label |

## 3. Connect Delhivery

From Delhivery:

1. **Delhivery One > Settings > API Setup**: copy the API token. Staging and
   production have different tokens.
2. Note the **warehouse (pickup location) name** exactly as registered. It is
   case-sensitive; a mismatch is the most common cause of a failed booking
   (`ClientWarehouse matching query does not exist`).

In OpenOMS: **Carriers > Add carrier > Delhivery**.

| Field | Value |
|---|---|
| API token | From step 1 |
| Warehouse name | Exactly as in step 2 |
| Seller name / GSTIN / default HSN code | Optional, printed on the packing slip |
| Default shipping mode | Surface (default) or Express |
| Label size | A4 or 4R (4x6 thermal). Empty uses the account default |
| Use staging | Tick when using a staging token |

Start with a **staging token** and "Use staging" ticked, book one test
shipment, then switch to the production token.

> When you edit a saved Delhivery integration, re-tick "Use staging" if it
> applies. The form cannot read back stored credentials, so an unticked box is
> saved as production.

## 4. Daily flow

1. **Create the order.** Fill the shipping address including **PIN code**,
   **State** and a **10-digit mobile number**. Set the payment method to `cod`
   for cash-on-delivery orders.
2. **Create a shipment** for the order and choose Shiprocket or Delhivery. On
   *Shipments > New*, the rate comparison lists every Shiprocket courier with
   its price; the "service" column is the courier ID.
3. **Generate label.** Enter the packed **weight** (required) and dimensions.
   - *COD amount (INR)* is pre-filled with the order total for unpaid COD
     orders. Leave it empty for prepaid orders. The courier collects exactly
     this amount.
   - Shiprocket: leave *Courier ID* empty for automatic selection, or type the
     ID from the rate comparison.
   - Delhivery: pick *Surface*, *Express* or the account default.
4. **Print the PDF label** and hand the parcel over.
5. **Tracking** updates on its own. Status mapping:

| OMS status | Shiprocket | Delhivery |
|---|---|---|
| `picked_up` | Picked Up | (first scan is In Transit) |
| `in_transit` | Shipped, In Transit, Reached Destination Hub, Undelivered (NDR) | In Transit, Pending |
| `out_for_delivery` | Out For Delivery | Dispatched |
| `delivered` | Delivered | Delivered |
| `returned` | RTO Delivered, RTO Acknowledged | RTO (delivered back) |
| `failed` | Lost, Damaged, Destroyed | Lost |

A parcel that is *on its way back* (RTO initiated / in transit) stays at its
last forward status until it actually reaches you, then becomes `returned`.
The raw courier status is always kept in the tracking history.

## Pickups

- **Shiprocket:** a pickup is requested for each label unless "I schedule
  pickups myself" is ticked. If the request fails, the label is still valid;
  the failure is logged and the pickup can be requested from the Shiprocket
  panel.
- **Delhivery:** OpenOMS does not raise pickup requests. Delhivery accepts one
  request per warehouse per slot, and most accounts have a standing daily
  pickup. Raise ad-hoc pickups in Delhivery One. (The SDK has
  `CreatePickupRequest` if you want to automate it later.)

## Rules the carriers enforce

OpenOMS checks these before calling the carrier and returns a plain error:

- Recipient PIN must be 6 digits; phone must be a 10-digit Indian number
  (`+91`, `0` prefixes and spaces are stripped).
- Domestic India only.
- Weight is required (or a default weight in the integration settings).
- Labels are PDF only (ZPL/EPL are not offered by either API).
- Shiprocket needs the recipient's state. If the address has none, it is
  derived from the PIN prefix. That fallback is approximate near a few state
  borders (for example Uttar Pradesh / Uttarakhand, Chandigarh / Mohali), so
  fill in the State field. Couriers route by PIN, so a wrong state never
  misroutes a parcel; it only appears on the label.

## Verification status

Tested here with automated tests against simulated carrier responses:
both SDKs, both carrier adapters, the label/COD/currency helpers and the
dashboard (type-check and unit tests).

**Not yet done: a call against the live Shiprocket and Delhivery APIs.** No
credentials were available. Endpoints, authentication and required fields
follow the carriers' published documentation, and response parsing tolerates
the known variants (numbers sent as strings, `remarks` as text or list), but
book one real shipment per carrier before going live. Things to confirm on
that first shipment:

1. Shiprocket: label creation right after AWB assignment, and that retrying a
   label after a failed courier assignment reuses the same Shiprocket order
   rather than creating a duplicate.
2. Delhivery: the packing-slip call returns a PDF link for your account
   (`pdf=true`); some accounts need this enabled by Delhivery.
3. Both: one COD shipment, checking the collectable amount on the label.

## Not included

- Carrier webhooks (push tracking). Tracking is polled.
- Reverse pickups / return shipments, NDR actions, multi-piece shipments.
- GST e-invoicing and e-way bills. OpenOMS invoicing is built for Poland
  (KSeF, Fakturownia); Shiprocket's own invoice PDF is available through the
  SDK (`PrintInvoice`).
- Weight-dispute and COD-remittance reconciliation.
