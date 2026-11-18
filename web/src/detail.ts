// Helpers for the application detail page, kept free of React so they can be
// unit-tested (detail.test.ts).
import type { Schemas } from "./api/client";

type Application = Schemas["Application"];
export type ApplicationFields = Schemas["ApplicationFields"];

// safeHref returns url if it is a plain http(s) link, else null (shown as text). The API
// only stores http(s) URLs; this is a second check before anything becomes a link.
export function safeHref(url: string | null | undefined): string | null {
  if (!url) {
    return null;
  }
  try {
    const u = new URL(url);
    return u.protocol === "https:" || u.protocol === "http:" ? url : null;
  } catch {
    return null;
  }
}

// formatSalary shows a salary range in the given currency, e.g. "₹18,00,000 – ₹25,00,000"
// for INR in an Indian locale. Empty when no salary is set.
export function formatSalary(
  min: number | null,
  max: number | null,
  currency: string | null,
  locale?: string,
): string {
  if (min === null && max === null) {
    return "";
  }
  let fmt: (n: number) => string;
  try {
    const nf = new Intl.NumberFormat(locale, { style: "currency", currency: currency ?? "", maximumFractionDigits: 0 });
    fmt = (n) => nf.format(n);
  } catch {
    fmt = (n) => `${n.toLocaleString(locale)} ${currency ?? ""}`.trim(); // unknown currency code
  }
  if (min !== null && max !== null) {
    return min === max ? fmt(min) : `${fmt(min)} – ${fmt(max)}`;
  }
  return min !== null ? `from ${fmt(min)}` : `up to ${fmt(max as number)}`;
}

// DetailsForm is the edit form's state: every field as typed.
export interface DetailsForm {
  role: string;
  job_url: string;
  source: string;
  location: string;
  salary_min: string;
  salary_max: string;
  salary_currency: string;
}

export function toForm(app: Application): DetailsForm {
  return {
    role: app.role,
    job_url: app.job_url ?? "",
    source: app.source ?? "",
    location: app.location ?? "",
    salary_min: app.salary_min?.toString() ?? "",
    salary_max: app.salary_max?.toString() ?? "",
    salary_currency: app.salary_currency ?? "",
  };
}

// fromForm turns the edit form into the API's request body: blank fields become null,
// salaries become whole numbers. The API does the real validation (e.g. currency
// required with a salary); this only catches what can't even be sent.
export function fromForm(
  form: DetailsForm,
  companyId: number,
): { fields: ApplicationFields; error?: never } | { error: string; fields?: never } {
  const salary = (s: string, name: string): number | null | string => {
    const t = s.replace(/[\s,_]/g, ""); // allow 18,00,000 or 1 800 000
    if (t === "") {
      return null;
    }
    return /^\d+$/.test(t) ? Number(t) : `${name} must be a whole number`;
  };
  const min = salary(form.salary_min, "Minimum salary");
  const max = salary(form.salary_max, "Maximum salary");
  if (typeof min === "string") {
    return { error: min };
  }
  if (typeof max === "string") {
    return { error: max };
  }
  const opt = (s: string) => (s.trim() === "" ? null : s.trim());
  return {
    fields: {
      company_id: companyId,
      role: form.role.trim(),
      job_url: opt(form.job_url),
      source: opt(form.source),
      location: opt(form.location),
      salary_min: min,
      salary_max: max,
      salary_currency: opt(form.salary_currency)?.toUpperCase() ?? null,
    },
  };
}

// occurredAt turns the stage-move form's date (yyyy-mm-dd, local) into the API's
// occurred_at. Today, or no date, means "now", which the API fills in. An earlier day is
// taken as noon local time that day, so no time zone can push it onto another day,
// unless that is before the previous stage change (lastChange; the API refuses a move
// dated before it), in which case it's a minute after that change.
export function occurredAt(date: string, today: Date, lastChange?: Date): string | undefined {
  if (!date) {
    return undefined;
  }
  const [y, m, d] = date.split("-").map(Number);
  if (!y || !m || !d) {
    return undefined;
  }
  const isToday = y === today.getFullYear() && m === today.getMonth() + 1 && d === today.getDate();
  if (isToday) {
    return undefined;
  }
  let when = new Date(y, m - 1, d, 12);
  if (lastChange && when < lastChange) {
    when = new Date(lastChange.getTime() + 60_000);
  }
  return when.toISOString();
}

// localDate formats a date as yyyy-mm-dd in local time (for <input type="date"> limits).
export function localDate(t: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())}`;
}
