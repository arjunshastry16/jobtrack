import { describe, expect, it } from "vitest";
import type { Schemas } from "./api/client";
import { findCompany } from "./companies";
import {
  boardPageURL,
  emptyWatchForm,
  formToInput,
  parseTerms,
  sortWatches,
  toggledInput,
  watchHealth,
  watchToForm,
} from "./watches";

type Watch = Schemas["Watch"];

function watch(over: Partial<Watch> = {}): Watch {
  return {
    id: 1, company_id: 1, board: "lever", board_token: "paytm", keywords: ["backend", "site reliability"],
    locations: ["india"], enabled: true, last_checked_at: "2026-11-18T00:30:00Z", last_error: null,
    created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z", ...over,
  };
}

describe("parseTerms", () => {
  it("splits on commas, trimming and dropping blanks", () => {
    expect(parseTerms(" backend, site reliability ,,\nGo ")).toEqual(["backend", "site reliability", "Go"]);
    expect(parseTerms("  ")).toEqual([]);
  });
});

describe("the watch form", () => {
  it("round-trips a watch through the form", () => {
    const w = watch();
    expect(formToInput(watchToForm(w, "Paytm"), w.company_id)).toEqual({
      company_id: 1, board: "lever", board_token: "paytm", keywords: ["backend", "site reliability"],
      locations: ["india"], enabled: true,
    });
  });

  it("trims the token and starts enabled with no filters", () => {
    const f = { ...emptyWatchForm(), token: "  stripe " };
    expect(formToInput(f, 3)).toEqual({ company_id: 3, board: "greenhouse", board_token: "stripe", keywords: [], locations: [], enabled: true });
  });
});

describe("toggledInput", () => {
  it("flips enabled and keeps everything else", () => {
    const w = watch({ enabled: false });
    expect(toggledInput(w)).toEqual({ ...formToInput(watchToForm(w, ""), w.company_id), enabled: true });
  });
});

describe("watchHealth", () => {
  it("puts paused before failing, and failing before waiting", () => {
    expect(watchHealth(watch({ enabled: false, last_error: "HTTP 404" }))).toBe("paused");
    expect(watchHealth(watch({ last_error: "HTTP 404" }))).toBe("failing");
    expect(watchHealth(watch({ last_checked_at: null }))).toBe("waiting");
    expect(watchHealth(watch())).toBe("ok");
  });
});

describe("boardPageURL", () => {
  it("links to each board's public jobs page", () => {
    expect(boardPageURL("greenhouse", "stripe")).toBe("https://job-boards.greenhouse.io/stripe");
    expect(boardPageURL("lever", "paytm")).toBe("https://jobs.lever.co/paytm");
    expect(boardPageURL("ashby", "sarvam")).toBe("https://jobs.ashbyhq.com/sarvam");
    expect(boardPageURL("smartrecruiters", "BoschGroup")).toBe("https://careers.smartrecruiters.com/BoschGroup");
    expect(boardPageURL("workday", "nvidia.wd5.myworkdayjobs.com/Site?q=go")).toBe("https://nvidia.wd5.myworkdayjobs.com/Site?q=go");
  });
  it("encodes anything unexpected in a token", () => {
    expect(boardPageURL("lever", "a/../b")).toBe("https://jobs.lever.co/a%2F..%2Fb");
  });
});

describe("sortWatches", () => {
  it("orders by company name, then board, then id", () => {
    const names = new Map([[1, "Zeta"], [2, "Acme"]]);
    const ws = [watch({ id: 1, company_id: 1 }), watch({ id: 2, company_id: 2, board: "lever" }), watch({ id: 3, company_id: 2, board: "ashby" }), watch({ id: 4, company_id: 2, board: "ashby" })];
    expect(sortWatches(ws, names).map((w) => w.id)).toEqual([3, 4, 2, 1]);
    expect(ws.map((w) => w.id)).toEqual([1, 2, 3, 4]); // the input is left alone
  });
});

describe("findCompany", () => {
  const cs = [{ id: 1, name: "NVIDIA" }, { id: 2, name: "Paytm" }].map((c) => ({ ...c, website: null, created_at: "", updated_at: "" }));
  it("matches ignoring case and spaces", () => {
    expect(findCompany(cs, "  nvidia ")?.id).toBe(1);
    expect(findCompany(cs, "Pay")).toBeUndefined();
  });
});
