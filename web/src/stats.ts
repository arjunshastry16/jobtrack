import type { Schemas } from "./api/client";
import type { Status } from "./stages";

type Stats = Schemas["Stats"];

// percent is part as a share of whole, 0–100, or null when whole is 0 (no rate to show).
export function percent(part: number, whole: number): number | null {
  return whole > 0 ? (part / whole) * 100 : null;
}

// formatPercent rounds to a whole percent; "–" when there's no rate.
export function formatPercent(p: number | null): string {
  return p === null ? "–" : `${Math.round(p)}%`;
}

// barWidth is a bar's length as a percentage of the longest bar (0 when every bar is 0).
export function barWidth(value: number, max: number): number {
  return max > 0 ? Math.min(100, (value / max) * 100) : 0;
}

export interface FunnelRow {
  stage: Status;
  reached: number;
  // Share of the previous stage's applications that got this far; null for the first
  // stage, or when nobody reached the previous one.
  ofPrevious: number | null;
}

// funnelRows adds the step-to-step conversion to each funnel stage.
export function funnelRows(funnel: Stats["funnel"]): FunnelRow[] {
  return funnel.map((f, i) => {
    const prev = funnel[i - 1];
    return { stage: f.stage, reached: f.reached, ofPrevious: prev ? percent(f.reached, prev.reached) : null };
  });
}

// responseRate is the share of applications that heard back after applying.
export function responseRate(stats: Stats): number | null {
  return percent(stats.responded, stats.funnel[0]?.reached ?? 0);
}

// reached is how many applications got to stage or later (0 for a stage not in the funnel).
export function reached(stats: Stats, stage: Status): number {
  return stats.funnel.find((f) => f.stage === stage)?.reached ?? 0;
}

// formatDays shows a duration in days: "under a day", "1 day", "5.4 days", "12 days".
export function formatDays(days: number | null): string {
  if (days === null) {
    return "–";
  }
  if (days < 1) {
    return "under a day";
  }
  const shown = days < 10 ? Math.round(days * 10) / 10 : Math.round(days);
  return shown === 1 ? "1 day" : `${shown} days`;
}

// axisTicks returns clean tick values (steps of 1, 2 or 5 × a power of ten) from 0 up to
// the first tick at or above max, about four steps in all.
export function axisTicks(max: number): number[] {
  if (!(max > 0)) {
    return [0, 1];
  }
  const raw = max / 4;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 5, 10].map((m) => m * magnitude).find((s) => s >= raw) ?? 10 * magnitude;
  const steps = Math.ceil(max / step);
  // Rounding keeps 0.1 + 0.2 from showing as 0.30000000000000004.
  return Array.from({ length: steps + 1 }, (_, i) => Number((i * step).toPrecision(12)));
}
