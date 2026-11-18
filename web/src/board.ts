// Board logic, kept free of React so it can be unit-tested (board.test.ts).
import type { Schemas } from "./api/client";
import { STAGES, type Status } from "./stages";

export type Application = Schemas["Application"];

// groupByStage puts applications into their columns, most recently updated first.
export function groupByStage(apps: readonly Application[]): Record<Status, Application[]> {
  const columns = Object.fromEntries(STAGES.map((s) => [s, [] as Application[]])) as Record<Status, Application[]>;
  for (const app of apps) {
    columns[app.status].push(app);
  }
  for (const s of STAGES) {
    columns[s].sort((a, b) => b.updated_at.localeCompare(a.updated_at) || b.id - a.id);
  }
  return columns;
}

// canDrop reports whether dragging app onto column `to` is a move the API allows. A
// drop on its own column is never a move (another interview round is in the card's
// menu instead, where it can't happen by accident).
export function canDrop(app: Application, to: Status): boolean {
  return to !== app.status && app.next_statuses.includes(to);
}

// moveLocally shows a move straight away, before the API confirms it. Until then the
// card has no further moves (next_statuses comes back with the API's answer).
export function moveLocally(apps: readonly Application[], id: number, to: Status): Application[] {
  return apps.map((a) => (a.id === id ? { ...a, status: to, next_statuses: [] } : a));
}

// replace swaps in the API's version of one application.
export function replace(apps: readonly Application[], updated: Application): Application[] {
  return apps.map((a) => (a.id === updated.id ? updated : a));
}

// moveLabel is how a move appears in a card's menu.
export function moveLabel(from: Status, to: Status, labels: Record<Status, string>): string {
  return from === to ? "Another round" : labels[to];
}
