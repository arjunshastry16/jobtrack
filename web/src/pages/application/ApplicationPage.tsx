import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, errorMessage, must, mustSucceed } from "../../api/client";
import { STAGE_LABELS } from "../../stages";
import { useLoad } from "../../useLoad";
import NotFound from "../NotFound";
import Contacts from "./Contacts";
import Details from "./Details";
import Notes from "./Notes";
import Stage from "./Stage";

// ApplicationPage is /applications/:id: everything about one application.
export default function ApplicationPage() {
  const id = Number(useParams().id);
  if (!Number.isSafeInteger(id) || id <= 0) {
    return <NotFound />;
  }
  // key: a different id is a different page, with fresh state.
  return <Loaded key={id} id={id} />;
}

function Loaded({ id }: { id: number }) {
  const navigate = useNavigate();
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const app = useLoad(
    async (signal) => must(await api.GET("/api/v1/applications/{id}", { params: { path: { id } }, signal })),
    [id],
  );

  if (app.error) {
    return (
      <section className="narrow">
        <p role="alert" className="error">
          Couldn't load this application: {app.error}
        </p>
        <p>
          <Link to="/">Back to the board</Link>
        </p>
      </section>
    );
  }
  if (!app.data) {
    return <p className="muted narrow">Loading…</p>;
  }
  const a = app.data;

  async function remove() {
    if (!window.confirm(`Delete "${a.role}" at ${a.company_name}, with its timeline and notes? This can't be undone.`)) {
      return;
    }
    try {
      mustSucceed(await api.DELETE("/api/v1/applications/{id}", { params: { path: { id } } }));
      void navigate("/");
    } catch (err) {
      setDeleteError(errorMessage(err));
    }
  }

  return (
    <article className="narrow detail">
      <p>
        <Link to="/">← Board</Link>
      </p>
      <header>
        <h1>{a.role}</h1>
        <p className="muted">
          {a.company_name} · <span className={`badge stage-${a.status}`}>{STAGE_LABELS[a.status]}</span>
        </p>
      </header>

      <Details app={a} onSaved={app.setData} />
      <Stage app={a} onMoved={app.setData} />
      <Notes applicationId={a.id} />
      <Contacts app={a} />

      <section className="danger">
        <button type="button" onClick={() => void remove()}>
          Delete application
        </button>
        {deleteError && (
          <p role="alert" className="error">
            {deleteError}
          </p>
        )}
      </section>
    </article>
  );
}
