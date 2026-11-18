// Picking a company by name, as typed into a form, or creating it if it's new.
import { api, must, type Schemas } from "./api/client";

type Company = Schemas["Company"];

// findCompany finds a company by name, ignoring case and surrounding spaces (company
// names are unique case-insensitively in the API too).
export function findCompany(companies: readonly Company[], name: string): Company | undefined {
  const n = name.trim().toLowerCase();
  return companies.find((c) => c.name.toLowerCase() === n);
}

// companyByName returns the known company with that name, or creates it. created says
// which, so the caller can add it to its list.
export async function companyByName(companies: readonly Company[], name: string): Promise<{ company: Company; created: boolean }> {
  const known = findCompany(companies, name);
  if (known) {
    return { company: known, created: false };
  }
  const company = must(await api.POST("/api/v1/companies", { body: { name: name.trim() } }));
  return { company, created: true };
}
