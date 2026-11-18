import { useEffect, useMemo, useState, type DragEvent } from "react";
import { Link } from "react-router";
import { api, errorMessage } from "../api/client";
import { canDrop, groupByStage, moveLabel, moveLocally, replace, type Application } from "../board";
import { STAGE_LABELS, STAGES, type Status } from "../stages";
import AddApplication from "./AddApplication";

// The API pages lists (at most 200 a page); the board wants everything. A personal job
// search won't come near this cap, which only stops a runaway loop.
const MAX_PAGES = 50;

async function loadAll(signal: AbortSignal): Promise<Application[]> {
  const all: Application[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < MAX_PAGES; page++) {
    const query = cursor ? { limit: 200, cursor } : { limit: 200 };
    const { data, error, response } = await api.GET("/api/v1/applications", { params: { query }, signal });
    if (!data) {
      throw new Error(errorMessage(error, response));
    }
    all.push(...data.applications);
    if (!data.next_cursor) {
      return all;
    }
    cursor = data.next_cursor;
  }
  return all;
}

// Board shows every application in a column per stage. A card moves by dragging it to
// another column (mouse) or with its "Move to…" menu (keyboard, touch). Only moves the
// API allows are offered; the card moves at once and goes back if the API refuses.
export default function Board() {
  const [apps, setApps] = useState<Application[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [dragging, setDragging] = useState<Application | null>(null);
  const [over, setOver] = useState<Status | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    setLoadError(null);
    loadAll(ctl.signal)
      .then(setApps)
      .catch((err: unknown) => {
        if (!ctl.signal.aborted) {
          setLoadError(errorMessage(err));
        }
      });
    return () => ctl.abort();
  }, [attempt]);

  const columns = useMemo(() => groupByStage(apps ?? []), [apps]);

  async function move(app: Application, to: Status) {
    setNotice(null);
    setApps((cur) => cur && moveLocally(cur, app.id, to));
    try {
      const { data, error, response } = await api.POST("/api/v1/applications/{id}/transitions", {
        params: { path: { id: app.id } },
        body: { to },
      });
      if (data) {
        setApps((cur) => cur && replace(cur, data.application));
        return;
      }
      throw new Error(errorMessage(error, response));
    } catch (err) {
      setApps((cur) => cur && replace(cur, app)); // put the card back as it was
      setNotice(`Couldn't move "${app.role}" to ${STAGE_LABELS[to]}: ${errorMessage(err)}`);
    }
  }

  function endDrag() {
    setDragging(null);
    setOver(null);
  }

  function columnHandlers(stage: Status) {
    const allowed = dragging !== null && canDrop(dragging, stage);
    return {
      onDragOver: (e: DragEvent) => {
        if (allowed) {
          e.preventDefault(); // allows the drop
          e.dataTransfer.dropEffect = "move";
          setOver(stage);
        }
      },
      onDragLeave: (e: DragEvent<HTMLElement>) => {
        // Moving onto a card inside the column also fires dragleave; ignore that.
        if (!(e.relatedTarget instanceof Node && e.currentTarget.contains(e.relatedTarget))) {
          setOver((o) => (o === stage ? null : o));
        }
      },
      onDrop: (e: DragEvent) => {
        e.preventDefault();
        const app = dragging;
        endDrag();
        if (app && canDrop(app, stage)) {
          void move(app, stage);
        }
      },
      className: ["column", allowed && "droppable", allowed && over === stage && "over"].filter(Boolean).join(" "),
    };
  }

  return (
    <section className="wide">
      <div className="page-head">
        <h1>Applications</h1>
        <AddApplication onAdded={(a) => setApps((cur) => (cur ? [a, ...cur] : [a]))} />
      </div>

      {notice && (
        <p role="alert" className="error">
          {notice}{" "}
          <button type="button" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </p>
      )}
      {loadError && (
        <p role="alert" className="error">
          Couldn't load applications: {loadError}{" "}
          <button type="button" onClick={() => setAttempt((n) => n + 1)}>
            Retry
          </button>
        </p>
      )}
      {apps === null && !loadError && <p className="muted">Loading…</p>}
      {apps?.length === 0 && <p className="muted">No applications yet. Add the first one above.</p>}

      {apps && (
        <div className="board">
          {STAGES.map((stage) => (
            <section key={stage} aria-label={STAGE_LABELS[stage]} {...columnHandlers(stage)}>
              <h2>
                {STAGE_LABELS[stage]} <span className="count">{columns[stage].length}</span>
              </h2>
              <ul>
                {columns[stage].map((app) => (
                  <Card key={app.id} app={app} onMove={move} onDragStart={() => setDragging(app)} onDragEnd={endDrag} />
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </section>
  );
}

function Card(props: {
  app: Application;
  onMove: (app: Application, to: Status) => Promise<void>;
  onDragStart: () => void;
  onDragEnd: () => void;
}) {
  const { app } = props;
  const movable = app.next_statuses.length > 0;
  return (
    <li
      className="card"
      draggable={movable}
      onDragStart={(e) => {
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", String(app.id)); // Firefox won't start a drag without data
        props.onDragStart();
      }}
      onDragEnd={props.onDragEnd}
    >
      {/* draggable={false}: dragging the card, not the link's URL */}
      <Link to={`/applications/${app.id}`} draggable={false} className="card-title">
        {app.role}
      </Link>
      <span className="muted">
        {app.company_name}
        {app.location ? ` · ${app.location}` : ""}
      </span>
      {movable && (
        <select
          aria-label={`Move ${app.role} at ${app.company_name}`}
          value=""
          onChange={(e) => {
            const to = app.next_statuses.find((s) => s === e.target.value);
            if (to) {
              void props.onMove(app, to);
            }
          }}
        >
          <option value="">Move to…</option>
          {app.next_statuses.map((s) => (
            <option key={s} value={s}>
              {moveLabel(app.status, s, STAGE_LABELS)}
            </option>
          ))}
        </select>
      )}
    </li>
  );
}
