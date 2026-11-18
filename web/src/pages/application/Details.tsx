import { useState, type FormEvent } from "react";
import { api, errorMessage, must, type Schemas } from "../../api/client";
import { formatSalary, fromForm, safeHref, toForm, type DetailsForm } from "../../detail";

type Application = Schemas["Application"];

// Details shows an application's fields, with an edit form.
export default function Details(props: { app: Application; onSaved: (app: Application) => void }) {
  const { app } = props;
  const [form, setForm] = useState<DetailsForm | null>(null); // null = not editing
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!form) {
      return;
    }
    const { fields, error: formError } = fromForm(form, app.company_id);
    if (formError !== undefined) {
      setError(formError);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      props.onSaved(must(await api.PUT("/api/v1/applications/{id}", { params: { path: { id: app.id } }, body: fields })));
      setForm(null);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!form) {
    const link = safeHref(app.job_url);
    const salary = formatSalary(app.salary_min, app.salary_max, app.salary_currency);
    return (
      <section>
        <h2>Details</h2>
        <dl className="fields">
          <dt>Job posting</dt>
          <dd>
            {link ? (
              <a href={link} target="_blank" rel="noopener noreferrer">
                {link}
              </a>
            ) : (
              (app.job_url ?? "—")
            )}
          </dd>
          <dt>Location</dt>
          <dd>{app.location ?? "—"}</dd>
          <dt>Source</dt>
          <dd>{app.source ?? "—"}</dd>
          <dt>Salary</dt>
          <dd>{salary || "—"}</dd>
        </dl>
        <button type="button" onClick={() => setForm(toForm(app))}>
          Edit details
        </button>
      </section>
    );
  }

  const field = (name: keyof DetailsForm, label: string, extra: object = {}) => (
    <label>
      {label}
      <input value={form[name]} onChange={(e) => setForm({ ...form, [name]: e.target.value })} {...extra} />
    </label>
  );
  return (
    <section>
      <h2>Details</h2>
      <form className="stacked" onSubmit={save}>
        {field("role", "Role", { required: true, maxLength: 200 })}
        {field("job_url", "Job posting URL", { type: "url", placeholder: "https://…" })}
        {field("location", "Location", { maxLength: 200 })}
        {field("source", "Source", { maxLength: 100, placeholder: "e.g. referral, LinkedIn, careers page" })}
        <div className="row">
          {field("salary_min", "Salary from", { inputMode: "numeric" })}
          {field("salary_max", "to", { inputMode: "numeric" })}
          {field("salary_currency", "Currency", { maxLength: 3, placeholder: "INR", size: 4 })}
        </div>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <div className="row">
          <button type="submit" disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </button>
          <button type="button" disabled={busy} onClick={() => (setForm(null), setError(null))}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}
