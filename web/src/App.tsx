import { useEffect, useState } from "react";
import { api, errorMessage, type Schemas } from "./api/client";

type Health = Schemas["Health"];
type Posting = Schemas["Posting"];

// A first page that proves the pieces fit together: the typed client, the dev proxy
// and the Go API. The real pages (board, application detail, inbox) come in later steps.
export default function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [postings, setPostings] = useState<Posting[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Aborting on unmount means a response arriving late can't update a page that's gone
    // (React's StrictMode also mounts twice in development, which this handles).
    const ctl = new AbortController();
    async function load() {
      const h = await api.GET("/healthz", { signal: ctl.signal });
      if (!h.data) {
        throw new Error(errorMessage(h.error, h.response));
      }
      setHealth(h.data);

      const p = await api.GET("/api/v1/postings", { params: { query: { limit: 10 } }, signal: ctl.signal });
      if (!p.data) {
        throw new Error(errorMessage(p.error, p.response));
      }
      setPostings(p.data.postings);
    }
    load().catch((err: unknown) => {
      if (!ctl.signal.aborted) {
        setError(errorMessage(err));
      }
    });
    return () => ctl.abort();
  }, []);

  return (
    <main>
      <h1>jobtrack</h1>
      <p className="muted">
        API: {health ? `${health.status} (version ${health.version})` : error ? "unreachable" : "checking…"}
      </p>
      {error && <p className="error">Couldn't load: {error}</p>}

      <h2>Newest postings</h2>
      {postings === null && !error && <p className="muted">Loading…</p>}
      {postings?.length === 0 && <p className="muted">Nothing yet. Add a watch and wait for the nightly sync.</p>}
      <ul className="postings">
        {postings?.map((p) => (
          <li key={p.id}>
            <a href={p.url} target="_blank" rel="noopener noreferrer">
              {p.title}
            </a>{" "}
            <span className="muted">
              · {p.company_name} · {p.location || "location not given"}
            </span>
          </li>
        ))}
      </ul>
    </main>
  );
}
