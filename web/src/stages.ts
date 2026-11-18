import type { Schemas } from "./api/client";

export type Status = Schemas["Status"];

// The board's columns, in pipeline order. The allowed moves between them come from the
// API (each application's next_statuses), so the Go state machine stays the only source
// of truth; only the order and labels live here.
export const STAGES = [
  "wishlist",
  "applied",
  "screening",
  "interviewing",
  "offer",
  "accepted",
  "rejected",
  "withdrawn",
] as const satisfies readonly Status[];

// Fails to compile if the API gains a stage that isn't listed above, naming it.
type Exhaustive<T extends never> = T;
export type AllStagesListed = Exhaustive<Exclude<Status, (typeof STAGES)[number]>>;

export const STAGE_LABELS: Record<Status, string> = {
  wishlist: "Wishlist",
  applied: "Applied",
  screening: "Screening",
  interviewing: "Interviewing",
  offer: "Offer",
  accepted: "Accepted",
  rejected: "Rejected",
  withdrawn: "Withdrawn",
};
