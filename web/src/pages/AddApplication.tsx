import { useEffect, useId, useState, type FormEvent } from "react";
import { api, errorMessage, type Schemas } from "../api/client";
import type { Application } from "../board";
import { companyByName } from "../companies";
import { localDate, occurredAt } from "../detail";

type Company = Schemas["Company"];

// AddApplication is a small form for a new application: pick an existing company (or
// type a new one, which is created) and a role, starting on the wishlist or as applied.
export default function AddApplication(props: { onAdded: (app: Application) => void }) {
  const [open, setOpen] = useState(false);
  const [companies, setCompanies] = useState<Company[]>([]);
  const [companyName, setCompanyName] = useState("");
  const [role, setRole] = useState("");
  const [status, setStatus] = useState<"wishlist" | "applied">("applied");
  const [date, setDate] = useState(""); // empty = today
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const listId = useId();

  useEffect(() => {
    if (!open) {
      return;
    }
    const ctl = new AbortController();
    api
      .GET("/api/v1/companies", { signal: ctl.signal })
      .then(({ data }) => data && setCompanies(data.companies))
      .catch(() => {}); // suggestions only; the form still works without them
    return () => ctl.abort();
  }, [open]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { company, created } = await companyByName(companies, companyName);
      if (created) {
        setCompanies((cs) => [...cs, company]);
      }
      // A date before today starts the timeline then, so reminders count from the day
      // you actually applied.
      const when = occurredAt(date, new Date());
      const { data, error, response } = await api.POST("/api/v1/applications", {
        body: { company_id: company.id, role: role.trim(), status, ...(when ? { occurred_at: when } : {}) },
      });
      if (!data) {
        throw new Error(errorMessage(error, response));
      }
      props.onAdded(data);
      setRole("");
      setDate("");
      setOpen(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)}>
        Add application
      </button>
    );
  }
  return (
    <form className="add-form" onSubmit={submit}>
      <label>
        Company
        <input required maxLength={200} list={listId} value={companyName} onChange={(e) => setCompanyName(e.target.value)} />
        <datalist id={listId}>
          {companies.map((c) => (
            <option key={c.id} value={c.name} />
          ))}
        </datalist>
      </label>
      <label>
        Role
        <input required maxLength={200} value={role} onChange={(e) => setRole(e.target.value)} />
      </label>
      <label>
        Stage
        <select value={status} onChange={(e) => setStatus(e.target.value === "wishlist" ? "wishlist" : "applied")}>
          <option value="applied">Applied</option>
          <option value="wishlist">Wishlist</option>
        </select>
      </label>
      <label>
        {status === "applied" ? "Applied on" : "Added on"}
        <input type="date" value={date} max={localDate(new Date())} onChange={(e) => setDate(e.target.value)} />
      </label>
      <button type="submit" disabled={busy}>
        {busy ? "Adding…" : "Add"}
      </button>
      <button type="button" onClick={() => setOpen(false)} disabled={busy}>
        Cancel
      </button>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </form>
  );
}
