// Helpers for the postings inbox and the watches page, kept free of React so they can be
// unit-tested (inbox.test.ts).
import type { Schemas } from "./api/client";

type Board = Schemas["Board"];
type Posting = Schemas["Posting"];
type Watch = Schemas["Watch"];

export const BOARD_LABELS: Record<Board, string> = {
  greenhouse: "Greenhouse",
  lever: "Lever",
  ashby: "Ashby",
  smartrecruiters: "SmartRecruiters",
  workday: "Workday",
};

// The inbox's filters live in the URL (?company=3&watch=7&removed=1), so a filtered view
// survives a reload and can be bookmarked.
export interface InboxFilters {
  companyId: number | null;
  watchId: number | null;
  includeRemoved: boolean;
}

// filtersFromParams reads the filters from the URL, ignoring values that aren't valid
// (a hand-edited ?watch=abc shows everything rather than an error).
export function filtersFromParams(params: URLSearchParams): InboxFilters {
  return {
    companyId: positiveInt(params.get("company")),
    watchId: positiveInt(params.get("watch")),
    includeRemoved: params.get("removed") === "1",
  };
}

// filtersToParams is the reverse, leaving out filters that aren't set.
export function filtersToParams(f: InboxFilters): URLSearchParams {
  const p = new URLSearchParams();
  if (f.companyId !== null) {
    p.set("company", String(f.companyId));
  }
  if (f.watchId !== null) {
    p.set("watch", String(f.watchId));
  }
  if (f.includeRemoved) {
    p.set("removed", "1");
  }
  return p;
}

// inboxQuery is the GET /postings query for one page.
export function inboxQuery(f: InboxFilters, cursor: string | null, limit: number) {
  return {
    limit,
    ...(f.companyId !== null ? { company_id: f.companyId } : {}),
    ...(f.watchId !== null ? { watch_id: f.watchId } : {}),
    ...(f.includeRemoved ? { include_removed: true } : {}),
    ...(cursor !== null ? { cursor } : {}),
  };
}

// At most 15 digits, so the number is exact (below Number.MAX_SAFE_INTEGER).
function positiveInt(s: string | null): number | null {
  if (s === null || !/^[1-9][0-9]{0,14}$/.test(s)) {
    return null;
  }
  return Number(s);
}

// markSaved records that a posting was saved as an application.
export function markSaved(postings: Posting[], postingId: number, applicationId: number): Posting[] {
  return postings.map((p) => (p.id === postingId ? { ...p, application_id: applicationId } : p));
}

// watchLabel names a watch for a picker: "NVIDIA · Workday".
export function watchLabel(w: Watch, companyNames: ReadonlyMap<number, string>): string {
  return `${companyNames.get(w.company_id) ?? `Company ${w.company_id}`} · ${BOARD_LABELS[w.board]}`;
}
