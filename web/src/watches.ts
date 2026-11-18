// Helpers for the watches page, kept free of React so they can be unit-tested
// (watches.test.ts).
import type { Schemas } from "./api/client";

type Board = Schemas["Board"];
type Watch = Schemas["Watch"];
export type WatchInput = Schemas["WatchInput"];

export const BOARDS: readonly Board[] = ["greenhouse", "lever", "ashby", "smartrecruiters", "workday"];

// What to type as a board's token, and where to find it.
export const TOKEN_HELP: Record<Board, { label: string; placeholder: string; hint: string }> = {
  greenhouse: {
    label: "Board token",
    placeholder: "e.g. stripe",
    hint: "The name after boards.greenhouse.io/ (or job-boards.greenhouse.io/) in the company's jobs page.",
  },
  lever: { label: "Company id", placeholder: "e.g. paytm", hint: "The name after jobs.lever.co/ in the company's jobs page." },
  ashby: { label: "Company id", placeholder: "e.g. sarvam", hint: "The name after jobs.ashbyhq.com/ in the company's jobs page." },
  smartrecruiters: {
    label: "Company id",
    placeholder: "e.g. BoschGroup",
    hint: "The name after jobs.smartrecruiters.com/ (or careers.smartrecruiters.com/) in a posting's link.",
  },
  workday: {
    label: "Careers-site URL",
    placeholder: "https://<company>.wd5.myworkdayjobs.com/<site>",
    hint:
      "Paste the careers page's address. Filter it first (e.g. Country: India) and only those postings are fetched. " +
      "Workday has no public API: this reads what the careers page itself loads, slowly, once a day.",
  },
};

// boardPageURL is the company's public jobs page on the board, to check a watch points
// at the right company. Board tokens are letters, digits and . _ - (Workday's is a host
// and path), so they are safe in a URL; encoding them is belt and braces.
export function boardPageURL(board: Board, token: string): string {
  const t = encodeURIComponent(token);
  switch (board) {
    case "greenhouse":
      return `https://job-boards.greenhouse.io/${t}`;
    case "lever":
      return `https://jobs.lever.co/${t}`;
    case "ashby":
      return `https://jobs.ashbyhq.com/${t}`;
    case "smartrecruiters":
      return `https://careers.smartrecruiters.com/${t}`;
    case "workday":
      return `https://${token}`; // stored as host/site?filters
  }
}

// parseTerms reads a comma-separated list ("backend, site reliability") as terms. The
// API normalizes them (lower case, whole words) and drops duplicates.
export function parseTerms(text: string): string[] {
  return text
    .split(",")
    .map((t) => t.trim())
    .filter((t) => t !== "");
}

export function formatTerms(terms: readonly string[]): string {
  return terms.join(", ");
}

// The watch form's fields, as typed.
export interface WatchForm {
  companyName: string;
  board: Board;
  token: string;
  keywords: string;
  locations: string;
  enabled: boolean;
}

export function emptyWatchForm(): WatchForm {
  return { companyName: "", board: "greenhouse", token: "", keywords: "", locations: "", enabled: true };
}

// watchToForm fills the form for editing a watch.
export function watchToForm(w: Watch, companyName: string): WatchForm {
  return {
    companyName,
    board: w.board,
    token: w.board_token,
    keywords: formatTerms(w.keywords),
    locations: formatTerms(w.locations),
    enabled: w.enabled,
  };
}

export function formToInput(f: WatchForm, companyId: number): WatchInput {
  return {
    company_id: companyId,
    board: f.board,
    board_token: f.token.trim(),
    keywords: parseTerms(f.keywords),
    locations: parseTerms(f.locations),
    enabled: f.enabled,
  };
}

// toggledInput is the PUT body that changes only whether the watch is enabled.
export function toggledInput(w: Watch): WatchInput {
  return {
    company_id: w.company_id,
    board: w.board,
    board_token: w.board_token,
    keywords: w.keywords,
    locations: w.locations,
    enabled: !w.enabled,
  };
}

export type WatchHealth = "paused" | "failing" | "waiting" | "ok";

// watchHealth sums up a watch for its badge: paused, failing (the last check went
// wrong), waiting (not checked since it was added) or ok.
export function watchHealth(w: Watch): WatchHealth {
  if (!w.enabled) {
    return "paused";
  }
  if (w.last_error !== null) {
    return "failing";
  }
  return w.last_checked_at === null ? "waiting" : "ok";
}

// sortWatches orders watches for the list: by company, then board.
export function sortWatches(ws: readonly Watch[], companyNames: ReadonlyMap<number, string>): Watch[] {
  const name = (w: Watch) => companyNames.get(w.company_id) ?? "";
  return [...ws].sort((a, b) => name(a).localeCompare(name(b)) || a.board.localeCompare(b.board) || a.id - b.id);
}
