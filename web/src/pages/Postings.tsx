import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { api, errorMessage, must, type Schemas } from "../api/client";
import { safeHref } from "../detail";
import { BOARD_LABELS, filtersFromParams, filtersToParams, inboxQuery, markSaved, watchLabel, type InboxFilters } from "../inbox";
import { formatDate, useLoad } from "../useLoad";

type Posting = Schemas["Posting"];

const PAGE_SIZE = 50;

// Postings is the inbox: postings the watches found, newest first, filtered by company or
// watch. "Save" puts a posting on the wishlist as an application; saved ones link to it.
export default function Postings() {
  const [params, setParams] = useSearchParams();
  const filters = filtersFromParams(params);
  // The filters as a string: one stable value for useLoad to compare between renders
  // (a fresh filters object every render would reload every render).
  const key = filtersToParams(filters).toString();

  const watches = useLoad(async (signal) => must(await api.GET("/api/v1/watches", { signal })).watches, []);
  const companies = useLoad(async (signal) => must(await api.GET("/api/v1/companies", { signal })).companies, []);
  const inbox = useLoad(
    async (signal) => {
      const page = must(await api.GET("/api/v1/postings", { params: { query: inboxQuery(filters, null, PAGE_SIZE) }, signal }));
      return { key, postings: page.postings, next: page.next_cursor };
    },
    [key],
  );
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<string | null>(null);

  const companyNames = useMemo(() => new Map((companies.data ?? []).map((c) => [c.id, c.name])), [companies.data]);
  // Only companies that have a watch can have postings.
  const watchedCompanies = useMemo(() => {
    const ids = new Set((watches.data ?? []).map((w) => w.company_id));
    return (companies.data ?? []).filter((c) => ids.has(c.id)).sort((a, b) => a.name.localeCompare(b.name));
  }, [watches.data, companies.data]);
  const watchOptions = (watches.data ?? []).filter((w) => filters.companyId === null || w.company_id === filters.companyId);

  function setFilters(f: InboxFilters) {
    setMoreError(null);
    setParams(filtersToParams(f));
  }

  // The list on screen is for the current filters only once its key matches; until then
  // the previous filters' list is still showing and the new one is loading.
  const current = inbox.data?.key === key ? inbox.data : null;

  async function loadMore() {
    if (!current?.next) {
      return;
    }
    setLoadingMore(true);
    setMoreError(null);
    try {
      const page = must(await api.GET("/api/v1/postings", { params: { query: inboxQuery(filters, current.next, PAGE_SIZE) } }));
      // Append only if the filters haven't changed meanwhile.
      inbox.setData((d) =>
        d && d.key === current.key ? { ...d, postings: [...d.postings, ...page.postings], next: page.next_cursor } : d,
      );
    } catch (err) {
      setMoreError(errorMessage(err));
    } finally {
      setLoadingMore(false);
    }
  }

  const filtered = filters.companyId !== null || filters.watchId !== null;
  return (
    <section className="narrow">
      <h1>Postings</h1>

      <div className="row filters">
        <label>
          Company
          <select
            value={filters.companyId ?? ""}
            onChange={(e) => {
              const companyId = e.target.value === "" ? null : Number(e.target.value);
              // A watch of another company would leave nothing to show, so it's cleared.
              const watch = watches.data?.find((w) => w.id === filters.watchId);
              const watchId = watch && (companyId === null || watch.company_id === companyId) ? watch.id : null;
              setFilters({ ...filters, companyId, watchId });
            }}
          >
            <option value="">All companies</option>
            {watchedCompanies.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Watch
          <select
            value={filters.watchId ?? ""}
            onChange={(e) => setFilters({ ...filters, watchId: e.target.value === "" ? null : Number(e.target.value) })}
          >
            <option value="">All watches</option>
            {watchOptions.map((w) => (
              <option key={w.id} value={w.id}>
                {watchLabel(w, companyNames)}
                {w.keywords.length > 0 ? ` (${w.keywords.join(", ")})` : ""}
              </option>
            ))}
          </select>
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={filters.includeRemoved}
            onChange={(e) => setFilters({ ...filters, includeRemoved: e.target.checked })}
          />
          Show postings no longer listed
        </label>
      </div>

      {(watches.error ?? companies.error) && (
        <p role="alert" className="error">
          Couldn't load the filters: {watches.error ?? companies.error}
        </p>
      )}
      {inbox.error && (
        <p role="alert" className="error">
          Couldn't load postings: {inbox.error}{" "}
          <button type="button" onClick={inbox.reload}>
            Retry
          </button>
        </p>
      )}
      {current === null && !inbox.error && <p className="muted">Loading…</p>}
      {current?.postings.length === 0 && (
        <p className="muted">
          {filtered ? (
            "No postings match these filters."
          ) : (
            <>
              No postings yet. <Link to="/watches">Watches</Link> are checked once a day; new matches show up here.
            </>
          )}
        </p>
      )}

      <ul className="postings">
        {current?.postings.map((p) => (
          <PostingItem
            key={p.id}
            posting={p}
            onSaved={(appId) => inbox.setData((d) => d && { ...d, postings: markSaved(d.postings, p.id, appId) })}
          />
        ))}
      </ul>

      {moreError && (
        <p role="alert" className="error">
          Couldn't load more: {moreError}
        </p>
      )}
      {current?.next && (
        <button type="button" disabled={loadingMore} onClick={() => void loadMore()}>
          {loadingMore ? "Loading…" : "Load more"}
        </button>
      )}
    </section>
  );
}

function PostingItem(props: { posting: Posting; onSaved: (applicationId: number) => void }) {
  const { posting: p } = props;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const href = safeHref(p.url);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const app = must(await api.POST("/api/v1/postings/{id}/application", { params: { path: { id: p.id } } }));
      props.onSaved(app.id);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className={p.removed_at ? "removed" : undefined}>
      <div className="posting-head">
        {href ? (
          <a href={href} target="_blank" rel="noopener noreferrer" className="posting-title">
            {p.title}
          </a>
        ) : (
          <span className="posting-title">{p.title}</span>
        )}
        {p.application_id !== null ? (
          <Link to={`/applications/${p.application_id}`} className="small">
            Saved · open
          </Link>
        ) : (
          <button type="button" disabled={busy} onClick={() => void save()}>
            {busy ? "Saving…" : "Save to wishlist"}
          </button>
        )}
      </div>
      <span className="muted small">
        {p.company_name} · {p.location || "location not given"} · {BOARD_LABELS[p.board]} · found{" "}
        {formatDate(p.first_seen_at)}
        {p.removed_at && <> · no longer listed since {formatDate(p.removed_at)}</>}
      </span>
      {error && (
        <p role="alert" className="error small">
          Couldn't save: {error}
        </p>
      )}
    </li>
  );
}
