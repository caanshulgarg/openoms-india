"use client";

import { useState } from "react";
import { toast } from "sonner";
import { ActionDialog } from "@/components/shared/action-dialog";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useGenerateLabel } from "@/hooks/use-shipments";
import { CarrierFields, type CarrierFieldValues } from "@/components/shipments/carrier-fields";
import { SHIPMENT_PROVIDER_LABELS } from "@/lib/constants";
import type { Order, Shipment, GenerateLabelRequest } from "@/types/api";
import { useTranslations } from "next-intl";

interface GenerateLabelDialogProps {
  shipmentId: string;
  provider: string;
  order: Order | undefined;
  shipment?: Shipment;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const DEFAULT_VALUES: Record<string, CarrierFieldValues> = {
  inpost: { service_type: "inpost_locker_standard", parcel_size: "small" },
  dhl: { service_type: "dhl_parcel" },
  dpd: { service_type: "dpd_classic" },
  gls: { service_type: "standard" },
  ups: { service_type: "11" },
  fedex: { service_type: "FEDEX_INTERNATIONAL_PRIORITY" },
  poczta_polska: { service_type: "POCZTEX_KURIER_48" },
  orlen_paczka: {},
  shiprocket: { service_type: "auto" },
  delhivery: { service_type: "standard" },
};

/** Carriers whose API only issues PDF labels. */
const PDF_ONLY_PROVIDERS = new Set(["shiprocket", "delhivery"]);

/** Indian carriers: COD is collected in INR and is pre-filled from the order. */
const INDIA_PROVIDERS = new Set(["shiprocket", "delhivery"]);

/**
 * For an unpaid cash-on-delivery order shipped with an Indian carrier, the COD
 * amount defaults to the order total so it is not forgotten on the label.
 */
function defaultCodAmount(provider: string, order: Order | undefined): number | undefined {
  if (!order || !INDIA_PROVIDERS.has(provider)) return undefined;
  const method = (order.payment_method ?? "").trim().toLowerCase();
  const isCod = method === "cod" || method === "cash on delivery" || method === "cash_on_delivery";
  if (!isCod || order.payment_status === "paid") return undefined;
  return order.total_amount > 0 ? order.total_amount : undefined;
}

function buildInitialValues(provider: string, shipment?: Shipment, order?: Order): CarrierFieldValues {
  const defaults: CarrierFieldValues = { ...(DEFAULT_VALUES[provider] ?? {}) };
  const cod = defaultCodAmount(provider, order);
  if (cod !== undefined) defaults.cod_amount = cod;
  const carrierData = shipment?.carrier_data as CarrierFieldValues | undefined;
  if (!carrierData) return defaults;
  // Merge: shipment carrier_data overrides defaults
  return { ...defaults, ...carrierData };
}

function getDialogTitle(provider: string, t: (key: string, params?: Record<string, string>) => string): string {
  const label = SHIPMENT_PROVIDER_LABELS[provider];
  if (label) return t("generateLabelFor", { carrier: label });
  return t("generateLabelFor", { carrier: provider.toUpperCase() });
}

export function GenerateLabelDialog({
  shipmentId,
  provider,
  order,
  shipment,
  open,
  onOpenChange,
}: GenerateLabelDialogProps) {
  const t = useTranslations("shipments");
  const generateLabel = useGenerateLabel(shipmentId);

  const [carrierValues, setCarrierValues] = useState<CarrierFieldValues>(
    () => buildInitialValues(provider, shipment, order)
  );
  const pdfOnly = PDF_ONLY_PROVIDERS.has(provider);
  const [labelFormat, setLabelFormat] = useState<string>("pdf");

  const handleFieldChange = (field: string, value: unknown) => {
    setCarrierValues((prev) => ({ ...prev, [field]: value }));
  };

  const isLocker =
    provider === "inpost" &&
    carrierValues.service_type === "inpost_locker_standard";
  const isSubmitDisabled =
    generateLabel.isPending ||
    (isLocker && !(carrierValues.target_point ?? "").trim());

  const handleSubmit = () => {
    const data: GenerateLabelRequest = {
      service_type: carrierValues.service_type ?? provider,
      label_format: labelFormat,
    };

    if (carrierValues.parcel_size) data.parcel_size = carrierValues.parcel_size;
    if (carrierValues.target_point) data.target_point = carrierValues.target_point.trim();
    if (carrierValues.weight_kg != null) data.weight_kg = carrierValues.weight_kg;
    if (carrierValues.width_cm != null) data.width_cm = carrierValues.width_cm;
    if (carrierValues.height_cm != null) data.height_cm = carrierValues.height_cm;
    if (carrierValues.depth_cm != null) data.depth_cm = carrierValues.depth_cm;
    if (carrierValues.cod_amount != null) data.cod_amount = carrierValues.cod_amount;
    if (carrierValues.insured_value != null) data.insured_value = carrierValues.insured_value;
    if (carrierValues.sending_method) data.sending_method = carrierValues.sending_method as string;

    generateLabel.mutate(data, {
      onSuccess: () => {
        toast.success(t("labelGenerated"));
        onOpenChange(false);
      },
      onError: (error) => {
        toast.error(error.message || t("labelGenerationError"));
      },
    });
  };

  return (
    <ActionDialog
      open={open}
      onOpenChange={onOpenChange}
      title={getDialogTitle(provider, t)}
      description={t("fillShipmentDataToGenerateLabel")}
      confirmLabel={t("generujEtykiete")}
      isPending={generateLabel.isPending}
      confirmDisabled={isSubmitDisabled}
      onConfirm={handleSubmit}
      contentClassName={isLocker ? "max-w-3xl" : ""}
    >
      <div className="space-y-4">
        {/* Carrier-specific fields */}
        <CarrierFields
          provider={provider}
          values={carrierValues}
          onChange={handleFieldChange}
        />

        {/* Label format — shared across all carriers */}
        <div className="space-y-2">
          <Label>{t("labelFormat")}</Label>
          <Select
            value={labelFormat}
            onValueChange={setLabelFormat}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="pdf">PDF</SelectItem>
              {!pdfOnly && <SelectItem value="zpl">{t("zplThermalPrinter")}</SelectItem>}
              {!pdfOnly && <SelectItem value="epl">{t("eplThermalPrinter")}</SelectItem>}
            </SelectContent>
          </Select>
        </div>

        {/* Receiver preview */}
        {order && (
          <div className="space-y-2 rounded-md border p-3">
            <Label>{t("receiver")}</Label>
            <div className="space-y-1 text-sm">
              <p>
                <span className="text-muted-foreground">{t("imieINazwisko")} </span>
                {order.customer_name}
              </p>
              <p>
                <span className="text-muted-foreground">{t("phone")}: </span>
                {order.customer_phone ?? "-"}
              </p>
              <p>
                <span className="text-muted-foreground">Email: </span>
                {order.customer_email ?? "-"}
              </p>
            </div>
            <p className="text-xs text-muted-foreground">
              {t("daneOdbiorcyPobraneZZamowienia")}
            </p>
          </div>
        )}
      </div>
    </ActionDialog>
  );
}
