import { describe, expect, it } from "vitest";
import { canDrop, groupByStage, moveLabel, moveLocally, replace, type Application } from "./board";
import { STAGE_LABELS, STAGES } from "./stages";

function app(id: number, status: Application["status"], next: Application["next_statuses"], updated = "2026-11-01T00:00:00Z"): Application {
  return {
    id, status, next_statuses: next, updated_at: updated, created_at: updated,
    company_id: 1, company_name: "Acme", role: "Engineer",
    job_url: null, source: null, location: null, salary_min: null, salary_max: null, salary_currency: null,
  };
}

describe("groupByStage", () => {
  it("puts every application in its column, newest first, with every column present", () => {
    const cols = groupByStage([
      app(1, "applied", [], "2026-11-01T00:00:00Z"),
      app(2, "applied", [], "2026-11-05T00:00:00Z"),
      app(3, "offer", []),
    ]);
    expect(Object.keys(cols)).toEqual([...STAGES]);
    expect(cols.applied.map((a) => a.id)).toEqual([2, 1]);
    expect(cols.offer.map((a) => a.id)).toEqual([3]);
    expect(cols.wishlist).toEqual([]);
  });

  it("breaks ties by id so the order is stable", () => {
    const cols = groupByStage([app(1, "applied", []), app(2, "applied", [])]);
    expect(cols.applied.map((a) => a.id)).toEqual([2, 1]);
  });
});

describe("canDrop", () => {
  const a = app(1, "interviewing", ["interviewing", "offer", "rejected", "withdrawn"]);
  it("allows the moves the API lists", () => {
    expect(canDrop(a, "offer")).toBe(true);
    expect(canDrop(a, "rejected")).toBe(true);
  });
  it("refuses moves the API doesn't list", () => {
    expect(canDrop(a, "wishlist")).toBe(false);
    expect(canDrop(a, "accepted")).toBe(false);
  });
  it("never treats a drop on the card's own column as a move", () => {
    expect(canDrop(a, "interviewing")).toBe(false);
  });
  it("refuses everything for a final stage", () => {
    const done = app(2, "rejected", []);
    expect(STAGES.some((s) => canDrop(done, s))).toBe(false);
  });
});

describe("optimistic moves", () => {
  it("moves the card at once and allows no further move until the API answers", () => {
    const before = [app(1, "applied", ["screening"]), app(2, "applied", ["screening"])];
    const after = moveLocally(before, 1, "screening");
    expect(after[0]).toMatchObject({ id: 1, status: "screening", next_statuses: [] });
    expect(after[1]).toBe(before[1]);
    expect(before[0]?.status).toBe("applied"); // the old list is untouched, so it can be restored
  });

  it("replaces the card with the API's version", () => {
    const fromAPI = app(1, "screening", ["interviewing", "rejected"]);
    expect(replace([app(1, "screening", [])], fromAPI)).toEqual([fromAPI]);
  });
});

describe("moveLabel", () => {
  it("names a move to the same stage as another round", () => {
    expect(moveLabel("interviewing", "interviewing", STAGE_LABELS)).toBe("Another round");
    expect(moveLabel("interviewing", "offer", STAGE_LABELS)).toBe("Offer");
  });
});
