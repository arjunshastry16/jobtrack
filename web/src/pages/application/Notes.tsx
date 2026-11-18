import { useState, type FormEvent } from "react";
import { api, errorMessage, must, mustSucceed, type Schemas } from "../../api/client";
import { formatDate, useLoad } from "../../useLoad";

type Note = Schemas["Note"];

// Notes lists an application's notes, oldest first, with add, edit and delete.
export default function Notes({ applicationId }: { applicationId: number }) {
  const notes = useLoad(
    async (signal) =>
      must(await api.GET("/api/v1/applications/{id}/notes", { params: { path: { id: applicationId } }, signal })).notes,
    [applicationId],
  );
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const note = must(
        await api.POST("/api/v1/applications/{id}/notes", { params: { path: { id: applicationId } }, body: { body: draft.trim() } }),
      );
      notes.setData((ns) => [...(ns ?? []), note]);
      setDraft("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section>
      <h2>Notes</h2>
      {notes.error && (
        <p role="alert" className="error">
          Couldn't load notes: {notes.error}
        </p>
      )}
      {notes.data?.length === 0 && <p className="muted">No notes yet.</p>}
      <ul className="notes">
        {notes.data?.map((n) => (
          <NoteItem
            key={n.id}
            note={n}
            onSaved={(saved) => notes.setData((ns) => ns && ns.map((x) => (x.id === saved.id ? saved : x)))}
            onDeleted={() => notes.setData((ns) => ns && ns.filter((x) => x.id !== n.id))}
          />
        ))}
      </ul>
      <form className="stacked" onSubmit={add}>
        <label>
          Add a note
          <textarea required rows={3} maxLength={10000} value={draft} onChange={(e) => setDraft(e.target.value)} />
        </label>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <div>
          <button type="submit" disabled={busy || draft.trim() === ""}>
            {busy ? "Adding…" : "Add note"}
          </button>
        </div>
      </form>
    </section>
  );
}

function NoteItem(props: { note: Note; onSaved: (n: Note) => void; onDeleted: () => void }) {
  const { note } = props;
  const [text, setText] = useState<string | null>(null); // null = not editing
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

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
  const save = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      props.onSaved(must(await api.PUT("/api/v1/notes/{id}", { params: { path: { id: note.id } }, body: { body: (text ?? "").trim() } })));
      setText(null);
    });
  };
  const remove = () => {
    if (window.confirm("Delete this note?")) {
      void run(async () => {
        mustSucceed(await api.DELETE("/api/v1/notes/{id}", { params: { path: { id: note.id } } }));
        props.onDeleted();
      });
    }
  };

  const edited = note.updated_at !== note.created_at;
  return (
    <li>
      {text === null ? (
        <>
          <p className="note-body">{note.body}</p>
          <p className="muted small">
            {formatDate(note.created_at)}
            {edited && ` · edited ${formatDate(note.updated_at)}`} ·{" "}
            <button type="button" className="link" disabled={busy} onClick={() => setText(note.body)}>
              Edit
            </button>{" "}
            <button type="button" className="link" disabled={busy} onClick={remove}>
              Delete
            </button>
          </p>
        </>
      ) : (
        <form className="stacked" onSubmit={save}>
          <textarea aria-label="Edit note" required rows={3} maxLength={10000} value={text} onChange={(e) => setText(e.target.value)} />
          <div className="row">
            <button type="submit" disabled={busy || text.trim() === ""}>
              Save
            </button>
            <button type="button" disabled={busy} onClick={() => setText(null)}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </li>
  );
}
