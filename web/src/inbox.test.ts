import { describe, expect, it } from "vitest";
import type { Schemas } from "./api/client";
import { filtersFromParams, filtersToParams, inboxQuery, markSaved, watchLabel } from "./inbox";

function posting(id: number, applicationId: number | null = null): Schemas["Posting"] {
  return {
    id, watch_id: 1, company_id: 1, company_name: "Acme", board: "lever", title: "Engineer", location: "",
    url: "https://jobs.example/1", published_at: null, first_seen_at: "2026-11-01T00:00:00Z",
    last_seen_at: "2026-11-01T00:00:00Z", removed_at: null, application_id: applicationId,
  };
}

describe("inbox filters", () => {
  it("round-trips through the URL", () => {
    const f = { companyId: 3, watchId: 7, includeRemoved: true };
    expect(filtersToParams(f).toString()).toBe("company=3&watch=7&removed=1");
    expect(filtersFromParams(filtersToParams(f))).toEqual(f);
  });

  it("leaves unset filters out of the URL", () => {
    expect(filtersToParams({ companyId: null, watchId: null, includeRemoved: false }).toString()).toBe("");
  });

  it("ignores values that aren't positive integers", () => {
    for (const bad of ["abc", "0", "-1", "1.5", "1e3", " 2", "", "9999999999999999"]) {
      expect(filtersFromParams(new URLSearchParams({ company: bad, watch: bad, removed: "yes" }))).toEqual({
        companyId: null,
        watchId: null,
        includeRemoved: false,
      });
    }
  });

  it("builds the API query, with only the filters that are set", () => {
    expect(inboxQuery({ companyId: null, watchId: null, includeRemoved: false }, null, 50)).toEqual({ limit: 50 });
    expect(inboxQuery({ companyId: 3, watchId: 7, includeRemoved: true }, "c1", 20)).toEqual({
      limit: 20, company_id: 3, watch_id: 7, include_removed: true, cursor: "c1",
    });
  });
});

describe("markSaved", () => {
  it("sets the application on that posting only", () => {
    const before = [posting(1), posting(2)];
    const after = markSaved(before, 2, 42);
    expect(after.map((p) => p.application_id)).toEqual([null, 42]);
    expect(before[1]?.application_id).toBeNull(); // the old list is unchanged
  });
});

describe("watchLabel", () => {
  const watch = (companyId: number): Schemas["Watch"] => ({
    id: 1, company_id: companyId, board: "workday", board_token: "https://x.wd5.myworkdayjobs.com/en-US/site",
    keywords: [], locations: [], enabled: true, last_checked_at: null, last_error: null,
    created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z",
  });
  it("names the company and board", () => {
    expect(watchLabel(watch(9), new Map([[9, "NVIDIA"]]))).toBe("NVIDIA · Workday");
  });
  it("falls back to the company id while names are loading", () => {
    expect(watchLabel(watch(9), new Map())).toBe("Company 9 · Workday");
  });
});
