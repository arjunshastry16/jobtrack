import { useEffect, useState } from "react";
import { api, errorMessage, type Schemas } from "../api/client";

type Posting = Schemas["Posting"];

// Postings lists the newest postings found by watches. Step 3.5 turns it into the full
// inbox (filters, paging, saving a posting as an application).
export default function Postings() {
  const [postings, setPostings] = useState<Posting[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const ctl = new AbortController();
    api
      .GET("/api/v1/postings", { params: { query: { limit: 20 } }, signal: ctl.signal })
      .then(({ data, error, response }) => {
        if (!data) {
          throw new Error(errorMessage(error, response));
        }
        setPostings(data.postings);
      })
      .catch((err: unknown) => {
        if (!ctl.signal.aborted) {
          setError(errorMessage(err));
        }
      });
    return () => ctl.abort();
  }, []);

  return (
    <section className="narrow">
      <h1>Newest postings</h1>
      {error && (
        <p role="alert" className="error">
          Couldn't load postings: {error}
        </p>
      )}
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
    </section>
  );
}
