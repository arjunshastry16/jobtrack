import { useEffect, useState } from "react";
import { NavLink, Route, Routes } from "react-router";
import { api } from "./api/client";
import ApplicationPage from "./pages/application/ApplicationPage";
import Board from "./pages/Board";
import NotFound from "./pages/NotFound";
import Postings from "./pages/Postings";

// The page layout and its routes. Routing happens in the browser; the Go server answers
// every non-API path with this app (see internal/webui), so deep links work.
export default function App() {
  return (
    <>
      <header className="topbar">
        <span className="brand">jobtrack</span>
        <nav>
          <NavLink to="/" end>
            Board
          </NavLink>
          <NavLink to="/postings">Postings</NavLink>
        </nav>
        <ApiVersion />
      </header>
      <main>
        <Routes>
          <Route path="/" element={<Board />} />
          <Route path="/applications/:id" element={<ApplicationPage />} />
          <Route path="/postings" element={<Postings />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
    </>
  );
}

// ApiVersion shows which build of the API is answering (handy after a deploy).
function ApiVersion() {
  const [version, setVersion] = useState<string | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    api
      .GET("/healthz", { signal: ctl.signal })
      .then(({ data }) => setVersion(data ? data.version : "unreachable"))
      .catch(() => {
        if (!ctl.signal.aborted) {
          setVersion("unreachable");
        }
      });
    return () => ctl.abort();
  }, []);
  return <span className="muted version">API {version ?? "…"}</span>;
}
