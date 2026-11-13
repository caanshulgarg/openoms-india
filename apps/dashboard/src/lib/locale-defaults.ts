// Regional defaults for new orders. OpenOMS started as a Polish OMS, so the
// fallbacks are PLN / PL; other deployments override them at build time with
// NEXT_PUBLIC_DEFAULT_CURRENCY / NEXT_PUBLIC_DEFAULT_COUNTRY (e.g. INR / IN).

function isoCode(value: string | undefined, length: number, fallback: string): string {
  const code = (value ?? "").trim().toUpperCase();
  return code.length === length && /^[A-Z]+$/.test(code) ? code : fallback;
}

/** ISO 4217 currency pre-filled on new orders. */
export const DEFAULT_CURRENCY = isoCode(process.env.NEXT_PUBLIC_DEFAULT_CURRENCY, 3, "PLN");

/** ISO 3166-1 alpha-2 country pre-filled on new addresses. */
export const DEFAULT_COUNTRY = isoCode(process.env.NEXT_PUBLIC_DEFAULT_COUNTRY, 2, "PL");
