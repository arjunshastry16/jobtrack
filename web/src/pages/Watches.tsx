import { useId, useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api, errorMessage, must, mustSucceed, type Schemas } from "../api/client";
import { companyByName } from "../companies";
import { safeHref } from "../detail";
import { BOARD_LABELS, watchLabel } from "../inbox";
import { formatDate, useLoad } from "../useLoad";
import {
  BOARDS,
  emptyWatchForm,
  formatTerms,
  formToInput,
  sortWatches,
  toggledInput,
  TOKEN_HELP,
  watchHealth,
  watchToForm,
  boardPageURL,
  type WatchForm as Form,
} from "../watches";

type Company = Schemas["Company"];
type Watch = Schemas["Watch"];

const HEALTH_LABELS = { paused: "Paused", failing: "Last check failed", waiting: "Not checked yet", ok: "OK" } as const;

// Watches lists the job boards being watched, and adds, edits, pauses and deletes them.
// Each watch is checked once a day; what it finds goes to the postings inbox.
export default function Watches() {
  const watches = useLoad(async (signal) => must(await api.GET("/api/v1/watches", { signal })).watches, []);
  const companies = useLoad(async (signal) => must(await api.GET("/api/v1/companies", { signal })).companies, []);
  const [adding, setAdding] = useState(false);

  const companyNames = useMemo(() => new Map((companies.data ?? []).map((c) => [c.id, c.name])), [companies.data]);
  const sorted = useMemo(() => sortWatches(watches.data ?? [], companyNames), [watches.data, companyNames]);

  const addCompany = (c: Company) => companies.setData((cs) => [...(cs ?? []), c]);
  const replaceWatch = (w: Watch) => watches.setData((ws) => ws && ws.map((x) => (x.id === w.id ? w : x)));

  const loadError = watches.error ?? companies.error;
  return (
    <section className="narrow">
      <div className="page-head">
        <h1>Watches</h1>
        {!adding && (
          <button type="button" onClick={() => setAdding(true)}>
            Add watch
          </button>
        )}
      </div>
      <p className="muted">
        A watch checks a company's job board once a day and puts new postings that match its keywords and locations in
        the <Link to="/postings">inbox</Link>.
      </p>

      {adding && (
        <WatchEditor
          companies={companies.data ?? []}
          onCompanyCreated={addCompany}
          onSaved={(w) => {
            watches.setData((ws) => [...(ws ?? []), w]);
            setAdding(false);
          }}
          onCancel={() => setAdding(false)}
        />
      )}

      {loadError && (
        <p role="alert" className="error">
          Couldn't load watches: {loadError}{" "}
          <button
            type="button"
            onClick={() => {
              watches.reload();
              companies.reload();
            }}
          >
            Retry
          </button>
        </p>
      )}
      {watches.data === null && !loadError && <p className="muted">Loading…</p>}
      {watches.data?.length === 0 && !adding && <p className="muted">No watches yet. Add one to start filling the inbox.</p>}

      <ul className="watches">
        {sorted.map((w) => (
          <WatchItem
            key={w.id}
            watch={w}
            companies={companies.data ?? []}
            companyNames={companyNames}
            onCompanyCreated={addCompany}
            onSaved={replaceWatch}
            onDeleted={() => watches.setData((ws) => ws && ws.filter((x) => x.id !== w.id))}
          />
        ))}
      </ul>
    </section>
  );
}

function WatchItem(props: {
  watch: Watch;
  companies: Company[];
  companyNames: ReadonlyMap<number, string>;
  onCompanyCreated: (c: Company) => void;
  onSaved: (w: Watch) => void;
  onDeleted: () => void;
}) {
  const { watch: w } = props;
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const label = watchLabel(w, props.companyNames);
  const health = watchHealth(w);
  const boardPage = safeHref(boardPageURL(w.board, w.board_token));

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }
  const toggle = () =>
    run(async () => {
      props.onSaved(must(await api.PUT("/api/v1/watches/{id}", { params: { path: { id: w.id } }, body: toggledInput(w) })));
    });
  const remove = () => {
    if (window.confirm(`Delete the ${label} watch? Its postings are deleted too; applications saved from them stay.`)) {
      void run(async () => {
        mustSucceed(await api.DELETE("/api/v1/watches/{id}", { params: { path: { id: w.id } } }));
        props.onDeleted();
      });
    }
  };

  if (editing) {
    return (
      <li>
        <WatchEditor
          watch={w}
          companies={props.companies}
          onCompanyCreated={props.onCompanyCreated}
          onSaved={(saved) => {
            props.onSaved(saved);
            setEditing(false);
          }}
          onCancel={() => setEditing(false)}
        />
      </li>
    );
  }
  return (
    <li className={w.enabled ? undefined : "paused"}>
      <div className="posting-head">
        <strong>{label}</strong>
        <span className={`badge health-${health}`}>{HEALTH_LABELS[health]}</span>
      </div>
      <dl className="fields small">
        <dt>Keywords</dt>
        <dd>{w.keywords.length > 0 ? formatTerms(w.keywords) : <span className="muted">any title</span>}</dd>
        <dt>Locations</dt>
        <dd>{w.locations.length > 0 ? formatTerms(w.locations) : <span className="muted">anywhere</span>}</dd>
        <dt>Board</dt>
        <dd>
          {boardPage ? (
            <a href={boardPage} target="_blank" rel="noopener noreferrer">
              {w.board_token}
            </a>
          ) : (
            w.board_token
          )}
        </dd>
        <dt>Last checked</dt>
        <dd>{w.last_checked_at ? formatDate(w.last_checked_at) : <span className="muted">not yet</span>}</dd>
        {w.last_error && (
          <>
            <dt>Last error</dt>
            <dd className="error">{w.last_error}</dd>
          </>
        )}
      </dl>
      <p className="small actions">
        <Link to={`/postings?watch=${w.id}`}>Postings</Link>
        {/* Editing shows the company's name, so it waits for the names to load. */}
        <button type="button" className="link" disabled={busy || !props.companyNames.has(w.company_id)} onClick={() => setEditing(true)}>
          Edit
        </button>
        <button type="button" className="link" disabled={busy} onClick={() => void toggle()}>
          {w.enabled ? "Pause" : "Resume"}
        </button>
        <button type="button" className="link" disabled={busy} onClick={remove}>
          Delete
        </button>
      </p>
      {error && (
        <p role="alert" className="error small">
          {error}
        </p>
      )}
    </li>
  );
}

// WatchEditor adds a watch, or edits one when given it. The company can be an existing one
// or a new name, which is created.
function WatchEditor(props: {
  watch?: Watch;
  companies: Company[];
  onCompanyCreated: (c: Company) => void;
  onSaved: (w: Watch) => void;
  onCancel: () => void;
}) {
  const { watch: w } = props;
  const [form, setForm] = useState<Form>(() =>
    w ? watchToForm(w, props.companies.find((c) => c.id === w.company_id)?.name ?? "") : emptyWatchForm(),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const listId = useId();
  const help = TOKEN_HELP[form.board];

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { company, created } = await companyByName(props.companies, form.companyName);
      if (created) {
        props.onCompanyCreated(company);
      }
      const body = formToInput(form, company.id);
      const saved = w
        ? must(await api.PUT("/api/v1/watches/{id}", { params: { path: { id: w.id } }, body }))
        : must(await api.POST("/api/v1/watches", { body }));
      props.onSaved(saved);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => ({ ...f, [k]: v }));
  return (
    <form className="stacked watch-form" onSubmit={submit}>
      <div className="row">
        <label>
          Company
          <input required maxLength={200} list={listId} value={form.companyName} onChange={(e) => set("companyName", e.target.value)} />
          <datalist id={listId}>
            {props.companies.map((c) => (
              <option key={c.id} value={c.name} />
            ))}
          </datalist>
        </label>
        <label>
          Board
          <select value={form.board} onChange={(e) => set("board", BOARDS.find((b) => b === e.target.value) ?? form.board)}>
            {BOARDS.map((b) => (
              <option key={b} value={b}>
                {BOARD_LABELS[b]}
                {b === "workday" ? " (unofficial)" : ""}
              </option>
            ))}
          </select>
        </label>
      </div>
      <label>
        {help.label}
        <input
          required
          maxLength={2000}
          value={form.token}
          placeholder={help.placeholder}
          // Text, not type="url": a saved Workday watch's address is stored without https://.
          inputMode={form.board === "workday" ? "url" : "text"}
          spellCheck={false}
          autoCapitalize="off"
          onChange={(e) => set("token", e.target.value)}
        />
        <span className="muted">{help.hint}</span>
      </label>
      <label>
        Keywords in the title (comma-separated; empty = any)
        <input value={form.keywords} placeholder="e.g. backend, site reliability, devops" onChange={(e) => set("keywords", e.target.value)} />
      </label>
      <label>
        Locations (comma-separated; empty = anywhere)
        <input value={form.locations} placeholder="e.g. india, bengaluru, remote" onChange={(e) => set("locations", e.target.value)} />
      </label>
      <label className="check">
        <input type="checkbox" checked={form.enabled} onChange={(e) => set("enabled", e.target.checked)} />
        Check it daily (untick to pause)
      </label>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <div className="row">
        <button type="submit" disabled={busy}>
          {busy ? "Saving…" : w ? "Save" : "Add watch"}
        </button>
        <button type="button" disabled={busy} onClick={props.onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
