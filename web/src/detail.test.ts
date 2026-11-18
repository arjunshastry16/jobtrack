import { describe, expect, it } from "vitest";
import { formatSalary, fromForm, localDate, occurredAt, safeHref, toForm, type DetailsForm } from "./detail";
import type { Application } from "./board";

describe("safeHref", () => {
  it("keeps http(s) links", () => {
    expect(safeHref("https://jobs.example/1")).toBe("https://jobs.example/1");
    expect(safeHref("http://jobs.example/1")).toBe("http://jobs.example/1");
  });
  it("refuses anything that could run code or isn't a URL", () => {
    for (const bad of ["javascript:alert(1)", "JAVASCRIPT:alert(1)", " javascript:alert(1)", "data:text/html,x", "vbscript:x", "jobs.example/1", "", null, undefined]) {
      expect(safeHref(bad)).toBeNull();
    }
  });
});

describe("formatSalary", () => {
  it("formats a range in the currency and locale", () => {
    expect(formatSalary(1800000, 2500000, "INR", "en-IN")).toBe("₹18,00,000 – ₹25,00,000");
    expect(formatSalary(120000, 150000, "USD", "en-US")).toBe("$120,000 – $150,000");
  });
  it("handles one-sided and equal ranges", () => {
    expect(formatSalary(1800000, null, "INR", "en-IN")).toBe("from ₹18,00,000");
    expect(formatSalary(null, 2500000, "INR", "en-IN")).toBe("up to ₹25,00,000");
    expect(formatSalary(100, 100, "USD", "en-US")).toBe("$100");
  });
  it("is empty without a salary, and survives an unknown currency code", () => {
    expect(formatSalary(null, null, null)).toBe("");
    expect(formatSalary(5, null, "XX", "en-US")).toBe("from 5 XX");
  });
});

describe("edit form", () => {
  const app: Application = {
    id: 1, company_id: 7, company_name: "Acme", role: "SRE", status: "applied", next_statuses: [],
    job_url: "https://jobs.example/1", source: null, location: "Pune", salary_min: 1800000, salary_max: null,
    salary_currency: "INR", created_at: "", updated_at: "",
  };

  it("round-trips an application", () => {
    expect(fromForm(toForm(app), 7)).toEqual({
      fields: {
        company_id: 7, role: "SRE", job_url: "https://jobs.example/1", source: null, location: "Pune",
        salary_min: 1800000, salary_max: null, salary_currency: "INR",
      },
    });
  });

  it("turns blanks into null, trims, and accepts grouped digits", () => {
    const form: DetailsForm = { ...toForm(app), role: "  Platform Engineer ", location: "  ", salary_min: "18,00,000", salary_max: "2 500 000", salary_currency: "inr" };
    const { fields } = fromForm(form, 7);
    expect(fields).toMatchObject({ role: "Platform Engineer", location: null, salary_min: 1800000, salary_max: 2500000, salary_currency: "INR" });
  });

  it("refuses salaries that aren't whole numbers", () => {
    expect(fromForm({ ...toForm(app), salary_min: "18 LPA" }, 7).error).toMatch(/Minimum salary/);
    expect(fromForm({ ...toForm(app), salary_max: "-5" }, 7).error).toMatch(/Maximum salary/);
  });
});

describe("occurredAt", () => {
  const today = new Date(2026, 10, 18, 9, 30); // 18 Nov 2026, local time
  it("leaves today (or no date) to the API's clock", () => {
    expect(occurredAt("", today)).toBeUndefined();
    expect(occurredAt("2026-11-18", today)).toBeUndefined();
  });
  it("puts an earlier day at local noon", () => {
    expect(occurredAt("2026-11-10", today)).toBe(new Date(2026, 10, 10, 12).toISOString());
  });
  it("never dates a move before the previous stage change", () => {
    const last = new Date(2026, 10, 10, 18, 0); // previous move: 10 Nov, 6 pm
    expect(occurredAt("2026-11-10", today, last)).toBe(new Date(2026, 10, 10, 18, 1).toISOString());
    expect(occurredAt("2026-11-12", today, last)).toBe(new Date(2026, 10, 12, 12).toISOString());
  });
  it("formats local dates for the date picker", () => {
    expect(localDate(today)).toBe("2026-11-18");
  });
});
