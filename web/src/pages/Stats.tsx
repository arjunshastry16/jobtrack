import type { CSSProperties, ReactNode } from "react";
import { Link } from "react-router";
import { api, must, type Schemas } from "../api/client";
import { STAGE_LABELS } from "../stages";
import { axisTicks, barWidth, formatDays, formatPercent, funnelRows, reached, responseRate } from "../stats";
import { useLoad } from "../useLoad";

type Stats = Schemas["Stats"];

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// Stats shows how the search is going: headline numbers, the funnel, how long each stage
// takes and where applications end. The charts are plain HTML and CSS: every row is
// focusable and shows its numbers on hover or focus, and each chart has a table view.
export default function StatsPage() {
  const stats = useLoad(async (signal) => must(await api.GET("/api/v1/stats", { signal })), []);
  const s = stats.data;

  return (
    <section className="narrow stats">
      <h1>Stats</h1>
      {stats.error && (
        <p role="alert" className="error">
          Couldn't load stats: {stats.error}{" "}
          <button type="button" onClick={stats.reload}>
            Retry
          </button>
        </p>
      )}
      {s === null && !stats.error && <p className="muted">Loading…</p>}
      {s?.applications === 0 && (
        <p className="muted">
          No applications yet. Add some on the <Link to="/">board</Link> and they're counted here.
        </p>
      )}
      {s !== null && s.applications > 0 && (
        <>
          <Tiles stats={s} />
          <Funnel stats={s} />
          <TimeInStage stats={s} />
          <Exits stats={s} />
          <ByStatus stats={s} />
        </>
      )}
    </section>
  );
}

function Tiles({ stats }: { stats: Stats }) {
  const applied = reached(stats, "applied");
  const rate = responseRate(stats);
  return (
    <dl className="tiles">
      <div>
        <dt>Applications</dt>
        <dd>{stats.applications}</dd>
      </div>
      <div>
        <dt>Applied</dt>
        <dd>{applied}</dd>
      </div>
      <div>
        <dt>Response rate</dt>
        <dd>{formatPercent(rate)}</dd>
        {rate !== null && <span className="muted small">{stats.responded} of {applied} heard back</span>}
      </div>
      <div>
        <dt>Offers</dt>
        <dd>{reached(stats, "offer")}</dd>
      </div>
    </dl>
  );
}

// Chart is a titled figure with a table view of the same numbers below it.
function Chart(props: { title: string; note: string; legend?: ReactNode; table: ReactNode; children: ReactNode }) {
  return (
    <figure className="chart">
      <figcaption>
        <h2>{props.title}</h2>
        <p className="muted small">{props.note}</p>
        {props.legend}
      </figcaption>
      {props.children}
      <details className="table-view">
        <summary className="small">Show as a table</summary>
        {props.table}
      </details>
    </figure>
  );
}

// BarRow is one horizontal bar: label, bar and the value at its tip. tip is what hover and
// focus show; it's also the row's accessible name.
function BarRow(props: { label: string; value: string; tip: string; children: ReactNode }) {
  return (
    <li className="chart-row" tabIndex={0} aria-label={props.tip}>
      <span className="row-label">{props.label}</span>
      <span className="track">{props.children}</span>
      <span className="row-value">{props.value}</span>
      <span className="tip" aria-hidden="true">
        {props.tip}
      </span>
    </li>
  );
}

const width = (pct: number): CSSProperties => ({ width: `${pct}%` });

function Funnel({ stats }: { stats: Stats }) {
  const rows = funnelRows(stats.funnel);
  const max = rows[0]?.reached ?? 0;
  const table = (
    <table>
      <thead>
        <tr>
          <th>Stage</th>
          <th>Reached</th>
          <th>Of the stage before</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.stage}>
            <td>{STAGE_LABELS[r.stage]}</td>
            <td>{r.reached}</td>
            <td>{formatPercent(r.ofPrevious)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
  return (
    <Chart
      title="Funnel"
      note="Applications that got to each stage or further. Skipping a stage counts as passing it."
      table={table}
    >
      {max === 0 ? (
        <p className="muted">Nothing applied for yet: the funnel starts at Applied.</p>
      ) : (
        <ul className="chart-rows">
          {rows.map((r) => (
            <BarRow
              key={r.stage}
              label={STAGE_LABELS[r.stage]}
              value={r.ofPrevious === null ? String(r.reached) : `${r.reached} · ${formatPercent(r.ofPrevious)}`}
              tip={`${STAGE_LABELS[r.stage]}: ${plural(r.reached, "application")}${
                r.ofPrevious === null ? "" : `, ${formatPercent(r.ofPrevious)} of the stage before`
              }`}
            >
              <span className="bar" style={width(barWidth(r.reached, max))} />
            </BarRow>
          ))}
        </ul>
      )}
    </Chart>
  );
}

function TimeInStage({ stats }: { stats: Stats }) {
  const rows = stats.time_in_stage;
  const ticks = axisTicks(Math.max(0, ...rows.map((r) => r.p90_days ?? 0)));
  const top = ticks.at(-1) ?? 1;
  const at = (days: number): CSSProperties => ({ left: `${barWidth(days, top)}%` });
  const table = (
    <table>
      <thead>
        <tr>
          <th>Stage</th>
          <th>Stays</th>
          <th>Median</th>
          <th>90th percentile</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.stage}>
            <td>{STAGE_LABELS[r.stage]}</td>
            <td>{r.stays}</td>
            <td>{formatDays(r.median_days)}</td>
            <td>{formatDays(r.p90_days)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
  const legend = (
    <ul className="legend small">
      <li>
        <span className="key dot median" /> Median
      </li>
      <li>
        <span className="key dot p90" /> 90th percentile
      </li>
    </ul>
  );
  return (
    <Chart
      title="Time in stage"
      note="How long finished stays in each stage lasted, in days. Applications still in a stage aren't counted yet."
      legend={legend}
      table={table}
    >
      <ul className="chart-rows">
        {rows.map((r) =>
          r.median_days === null || r.p90_days === null ? (
            <li key={r.stage} className="chart-row">
              <span className="row-label">{STAGE_LABELS[r.stage]}</span>
              <span className="muted small span-rest">No finished stays yet</span>
            </li>
          ) : (
            <BarRow
              key={r.stage}
              label={STAGE_LABELS[r.stage]}
              value={formatDays(r.median_days)}
              tip={`${STAGE_LABELS[r.stage]}: median ${formatDays(r.median_days)}, 90th percentile ${formatDays(
                r.p90_days,
              )} (${plural(r.stays, "stay")})`}
            >
              <Grid ticks={ticks} top={top} />
              <span
                className="range"
                style={{ ...at(r.median_days), width: `${barWidth(r.p90_days, top) - barWidth(r.median_days, top)}%` }}
              />
              <span className="dot p90" style={at(r.p90_days)} />
              <span className="dot median" style={at(r.median_days)} />
            </BarRow>
          ),
        )}
        <li className="chart-row axis" aria-hidden="true">
          <span className="row-label" />
          <span className="track">
            {ticks.map((t) => (
              <span key={t} className="tick" style={at(t)}>
                {t}
              </span>
            ))}
          </span>
          <span className="row-value muted small">days</span>
        </li>
      </ul>
    </Chart>
  );
}

// Grid draws the vertical gridlines behind one row of a chart with a value axis.
function Grid(props: { ticks: number[]; top: number }) {
  return props.ticks.map((t) => <span key={t} className="gridline" style={{ left: `${barWidth(t, props.top)}%` }} />);
}

function Exits({ stats }: { stats: Stats }) {
  const rows = stats.exits.map((e) => ({ ...e, total: e.rejected + e.withdrawn }));
  const max = Math.max(0, ...rows.map((r) => r.total));
  const table = (
    <table>
      <thead>
        <tr>
          <th>Left from</th>
          <th>Rejected</th>
          <th>Withdrawn</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.stage}>
            <td>{STAGE_LABELS[r.stage]}</td>
            <td>{r.rejected}</td>
            <td>{r.withdrawn}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
  const legend = (
    <ul className="legend small">
      <li>
        <span className="key swatch rejected" /> Rejected
      </li>
      <li>
        <span className="key swatch withdrawn" /> Withdrawn
      </li>
    </ul>
  );
  return (
    <Chart title="Where applications ended" note="The stage each rejection or withdrawal happened at." legend={legend} table={table}>
      {max === 0 ? (
        <p className="muted">No rejections or withdrawals yet.</p>
      ) : (
        <ul className="chart-rows">
          {rows.map((r) => (
            <BarRow
              key={r.stage}
              label={STAGE_LABELS[r.stage]}
              value={String(r.total)}
              tip={`From ${STAGE_LABELS[r.stage]}: ${r.rejected} rejected, ${r.withdrawn} withdrawn`}
            >
              <span className="stack" style={width(barWidth(r.total, max))}>
                {r.rejected > 0 && <span className="bar rejected" style={{ flexGrow: r.rejected }} />}
                {r.withdrawn > 0 && <span className="bar withdrawn" style={{ flexGrow: r.withdrawn }} />}
              </span>
            </BarRow>
          ))}
        </ul>
      )}
    </Chart>
  );
}

function ByStatus({ stats }: { stats: Stats }) {
  const max = Math.max(0, ...stats.by_status.map((b) => b.applications));
  const table = (
    <table>
      <thead>
        <tr>
          <th>Stage</th>
          <th>Applications</th>
        </tr>
      </thead>
      <tbody>
        {stats.by_status.map((b) => (
          <tr key={b.status}>
            <td>{STAGE_LABELS[b.status]}</td>
            <td>{b.applications}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
  return (
    <Chart title="Where things stand" note="Applications by their current stage." table={table}>
      <ul className="chart-rows">
        {stats.by_status.map((b) => (
          <BarRow
            key={b.status}
            label={STAGE_LABELS[b.status]}
            value={String(b.applications)}
            tip={`${STAGE_LABELS[b.status]} now: ${plural(b.applications, "application")}`}
          >
            <span className="bar" style={width(barWidth(b.applications, max))} />
          </BarRow>
        ))}
      </ul>
    </Chart>
  );
}
