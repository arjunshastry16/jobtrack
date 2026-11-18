import { useState, type FormEvent } from "react";
import { api, errorMessage, must, type Schemas } from "../../api/client";
import { moveLabel } from "../../board";
import { localDate, occurredAt } from "../../detail";
import { STAGE_LABELS, type Status } from "../../stages";
import { formatDate, useLoad } from "../../useLoad";

type Application = Schemas["Application"];

// Stage moves the application on (with an optional note and date) and shows its timeline:
// every stage change, newest first.
export default function Stage(props: { app: Application; onMoved: (app: Application) => void }) {
  const { app } = props;
  const timeline = useLoad(
    async (signal) =>
      must(await api.GET("/api/v1/applications/{id}/timeline", { params: { path: { id: app.id } }, signal })).events,
    [app.id],
  );
  const [to, setTo] = useState<Status | "">("");
  const [note, setNote] = useState("");
  const [date, setDate] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function move(e: FormEvent) {
    e.preventDefault();
    if (!to) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const when = occurredAt(date, new Date(), lastChange);
      const trimmed = note.trim();
      const res = must(
        await api.POST("/api/v1/applications/{id}/transitions", {
          params: { path: { id: app.id } },
          body: { to, ...(trimmed ? { note: trimmed } : {}), ...(when ? { occurred_at: when } : {}) },
        }),
      );
      props.onMoved(res.application);
      timeline.setData((events) => (events ? [...events, res.event] : [res.event]));
      setTo("");
      setNote("");
      setDate("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const events = timeline.data ? [...timeline.data].reverse() : null; // newest first
  // A move can't be dated before the previous one, so the date picker starts there.
  const latest = timeline.data?.at(-1)?.occurred_at;
  const lastChange = latest ? new Date(latest) : undefined;
  return (
    <section>
      <h2>Stage</h2>
      {app.next_statuses.length > 0 ? (
        <form className="stacked" onSubmit={move}>
          <div className="row">
            <label>
              Move to
              <select required value={to} onChange={(e) => setTo(app.next_statuses.find((s) => s === e.target.value) ?? "")}>
                <option value="">Choose…</option>
                {app.next_statuses.map((s) => (
                  <option key={s} value={s}>
                    {moveLabel(app.status, s, STAGE_LABELS)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              On
              <input
                type="date"
                value={date}
                min={lastChange ? localDate(lastChange) : undefined}
                max={localDate(new Date())}
                onChange={(e) => setDate(e.target.value)}
              />
            </label>
          </div>
          <label>
            Note (optional)
            <input value={note} maxLength={2000} placeholder="e.g. recruiter call, 45 min" onChange={(e) => setNote(e.target.value)} />
          </label>
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          <div>
            <button type="submit" disabled={busy || !to}>
              {busy ? "Moving…" : "Move"}
            </button>
          </div>
        </form>
      ) : (
        <p className="muted">{STAGE_LABELS[app.status]} is a final stage.</p>
      )}

      <h3>Timeline</h3>
      {timeline.error && (
        <p role="alert" className="error">
          Couldn't load the timeline: {timeline.error}
        </p>
      )}
      {events === null && !timeline.error && <p className="muted">Loading…</p>}
      <ol className="timeline">
        {events?.map((ev) => (
          <li key={ev.id}>
            <time dateTime={ev.occurred_at} title={new Date(ev.occurred_at).toLocaleString()}>
              {formatDate(ev.occurred_at)}
            </time>{" "}
            {ev.from_status ? (
              <>
                {STAGE_LABELS[ev.from_status]} → <strong>{STAGE_LABELS[ev.to_status]}</strong>
              </>
            ) : (
              <>
                Added as <strong>{STAGE_LABELS[ev.to_status]}</strong>
              </>
            )}
            {ev.note && <p className="muted">{ev.note}</p>}
          </li>
        ))}
      </ol>
    </section>
  );
}
