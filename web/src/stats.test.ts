import { describe, expect, it } from "vitest";
import type { Schemas } from "./api/client";
import { axisTicks, barWidth, formatDays, formatPercent, funnelRows, percent, reached, responseRate } from "./stats";

function stats(funnel: number[], responded: number): Schemas["Stats"] {
  const stages = ["applied", "screening", "interviewing", "offer", "accepted"] as const;
  return {
    applications: (funnel[0] ?? 0) + 2, // two more on the wishlist, never applied
    by_status: [],
    funnel: stages.map((stage, i) => ({ stage, reached: funnel[i] ?? 0 })),
    responded,
    time_in_stage: [],
    exits: [],
  };
}

describe("percent", () => {
  it("is part of whole out of 100", () => {
    expect(percent(1, 4)).toBe(25);
    expect(percent(4, 4)).toBe(100);
    expect(percent(0, 4)).toBe(0);
  });

  it("is null when there's nothing to divide by", () => {
    expect(percent(0, 0)).toBeNull();
  });

  it("formats as a whole percent", () => {
    expect(formatPercent(42.857)).toBe("43%");
    expect(formatPercent(0)).toBe("0%");
    expect(formatPercent(null)).toBe("–");
  });
});

describe("barWidth", () => {
  it("is the value's share of the longest bar", () => {
    expect(barWidth(5, 10)).toBe(50);
    expect(barWidth(10, 10)).toBe(100);
  });

  it("is 0 when every bar is 0, and never past 100", () => {
    expect(barWidth(0, 0)).toBe(0);
    expect(barWidth(12, 10)).toBe(100);
  });
});

describe("funnelRows", () => {
  it("adds the share of the previous stage", () => {
    const rows = funnelRows(stats([5, 2, 2, 1, 0], 3).funnel);
    expect(rows.map((r) => [r.stage, r.reached, r.ofPrevious])).toEqual([
      ["applied", 5, null],
      ["screening", 2, 40],
      ["interviewing", 2, 100],
      ["offer", 1, 50],
      ["accepted", 0, 0],
    ]);
  });

  it("has no conversion after a stage nobody reached", () => {
    const rows = funnelRows(stats([1, 0, 0, 0, 0], 0).funnel);
    expect(rows[2]?.ofPrevious).toBeNull();
  });
});

describe("responseRate and reached", () => {
  it("is responded out of applied", () => {
    expect(responseRate(stats([5, 2, 2, 1, 0], 3))).toBe(60);
    expect(responseRate(stats([0, 0, 0, 0, 0], 0))).toBeNull();
  });

  it("reads one stage of the funnel", () => {
    expect(reached(stats([5, 2, 2, 1, 0], 3), "offer")).toBe(1);
    expect(reached(stats([5, 2, 2, 1, 0], 3), "wishlist")).toBe(0);
  });
});

describe("formatDays", () => {
  it("rounds to a tenth under ten days and to whole days above", () => {
    expect(formatDays(5.44)).toBe("5.4 days");
    expect(formatDays(3)).toBe("3 days");
    expect(formatDays(9.96)).toBe("10 days");
    expect(formatDays(12.6)).toBe("13 days");
  });

  it("says day for one, and under a day below it", () => {
    expect(formatDays(1)).toBe("1 day");
    expect(formatDays(1.04)).toBe("1 day");
    expect(formatDays(0.4)).toBe("under a day");
    expect(formatDays(0)).toBe("under a day");
    expect(formatDays(null)).toBe("–");
  });
});

describe("axisTicks", () => {
  it("steps by 1, 2 or 5 times a power of ten up past the max", () => {
    expect(axisTicks(8.8)).toEqual([0, 5, 10]);
    expect(axisTicks(8)).toEqual([0, 2, 4, 6, 8]);
    expect(axisTicks(3)).toEqual([0, 1, 2, 3]);
    expect(axisTicks(37)).toEqual([0, 10, 20, 30, 40]);
    expect(axisTicks(120)).toEqual([0, 50, 100, 150]);
  });

  it("handles fractions of a day without float noise", () => {
    expect(axisTicks(0.3)).toEqual([0, 0.1, 0.2, 0.3]);
  });

  it("has a unit axis when there's nothing to show", () => {
    expect(axisTicks(0)).toEqual([0, 1]);
    expect(axisTicks(Number.NaN)).toEqual([0, 1]);
  });
});
