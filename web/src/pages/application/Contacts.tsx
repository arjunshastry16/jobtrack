import { useState, type FormEvent } from "react";
import { api, errorMessage, must, mustSucceed, type Schemas } from "../../api/client";
import { safeHref } from "../../detail";
import { useLoad } from "../../useLoad";

type Application = Schemas["Application"];
type Contact = Schemas["Contact"];

// Contacts shows the people linked to an application (recruiter, hiring manager, …),
// and links one of the company's contacts or a new one.
export default function Contacts({ app }: { app: Application }) {
  const linked = useLoad(
    async (signal) =>
      must(await api.GET("/api/v1/applications/{id}/contacts", { params: { path: { id: app.id } }, signal })).contacts,
    [app.id],
  );
  const companyContacts = useLoad(
    async (signal) =>
      must(await api.GET("/api/v1/contacts", { params: { query: { company_id: app.company_id } }, signal })).contacts,
    [app.company_id],
  );
  const [pick, setPick] = useState("");
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const linkedIds = new Set((linked.data ?? []).map((c) => c.id));
  const linkable = (companyContacts.data ?? []).filter((c) => !linkedIds.has(c.id));

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
  const link = (c: Contact) =>
    run(async () => {
      mustSucceed(
        await api.PUT("/api/v1/applications/{id}/contacts/{contact_id}", { params: { path: { id: app.id, contact_id: c.id } } }),
      );
      linked.setData((cs) => [...(cs ?? []), c].sort((x, y) => x.name.localeCompare(y.name)));
      setPick("");
    });
  const unlink = (c: Contact) =>
    run(async () => {
      mustSucceed(
        await api.DELETE("/api/v1/applications/{id}/contacts/{contact_id}", { params: { path: { id: app.id, contact_id: c.id } } }),
      );
      linked.setData((cs) => cs && cs.filter((x) => x.id !== c.id));
    });

  return (
    <section>
      <h2>Contacts</h2>
      {(linked.error ?? companyContacts.error) && (
        <p role="alert" className="error">
          Couldn't load contacts: {linked.error ?? companyContacts.error}
        </p>
      )}
      {linked.data?.length === 0 && <p className="muted">No contacts linked.</p>}
      <ul className="contacts">
        {linked.data?.map((c) => (
          <li key={c.id}>
            <ContactLine contact={c} />{" "}
            <button type="button" className="link" disabled={busy} onClick={() => void unlink(c)}>
              Unlink
            </button>
          </li>
        ))}
      </ul>

      <div className="row">
        {linkable.length > 0 && (
          <>
            <select aria-label={`Link a contact at ${app.company_name}`} value={pick} onChange={(e) => setPick(e.target.value)}>
              <option value="">Link a contact at {app.company_name}…</option>
              {linkable.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                  {c.title ? ` (${c.title})` : ""}
                </option>
              ))}
            </select>
            <button
              type="button"
              disabled={busy || !pick}
              onClick={() => {
                const c = linkable.find((x) => String(x.id) === pick);
                if (c) {
                  void link(c);
                }
              }}
            >
              Link
            </button>
          </>
        )}
        {!adding && (
          <button type="button" onClick={() => setAdding(true)}>
            New contact
          </button>
        )}
      </div>

      {adding && (
        <NewContact
          companyId={app.company_id}
          onCancel={() => setAdding(false)}
          onCreated={(c) => {
            setAdding(false);
            companyContacts.setData((cs) => [...(cs ?? []), c]);
            void link(c);
          }}
        />
      )}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </section>
  );
}

function ContactLine({ contact: c }: { contact: Contact }) {
  const linkedin = safeHref(c.linkedin_url);
  return (
    <>
      <strong>{c.name}</strong>
      {c.title && <span className="muted"> · {c.title}</span>}
      {c.email && (
        <>
          {" · "}
          <a href={`mailto:${c.email}`}>{c.email}</a>
        </>
      )}
      {linkedin && (
        <>
          {" · "}
          <a href={linkedin} target="_blank" rel="noopener noreferrer">
            LinkedIn
          </a>
        </>
      )}
    </>
  );
}

function NewContact(props: { companyId: number; onCreated: (c: Contact) => void; onCancel: () => void }) {
  const [form, setForm] = useState({ name: "", title: "", email: "", linkedin_url: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const opt = (s: string) => (s.trim() === "" ? null : s.trim());
    try {
      props.onCreated(
        must(
          await api.POST("/api/v1/contacts", {
            body: {
              company_id: props.companyId,
              name: form.name.trim(),
              title: opt(form.title),
              email: opt(form.email),
              linkedin_url: opt(form.linkedin_url),
            },
          }),
        ),
      );
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const input = (name: keyof typeof form, label: string, extra: object = {}) => (
    <label>
      {label}
      <input value={form[name]} onChange={(e) => setForm({ ...form, [name]: e.target.value })} {...extra} />
    </label>
  );
  return (
    <form className="stacked" onSubmit={submit}>
      {input("name", "Name", { required: true, maxLength: 200 })}
      {input("title", "Title", { placeholder: "e.g. Recruiter" })}
      {input("email", "Email", { type: "email" })}
      {input("linkedin_url", "LinkedIn URL", { type: "url", placeholder: "https://www.linkedin.com/in/…" })}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <div className="row">
        <button type="submit" disabled={busy}>
          {busy ? "Adding…" : "Add and link"}
        </button>
        <button type="button" disabled={busy} onClick={props.onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
