import { useEffect, useState } from "react";
import { sql } from "@codemirror/lang-sql";
import CodeMirror from "@uiw/react-codemirror";
import { migrationApi, type Migration, type MigrationRun } from "./api";

type Props = { databaseId: number; databaseKind: "postgres"|"mysql"; effectiveLevel: ""|"view"|"edit" };

export function MigrationsPage({databaseId,databaseKind,effectiveLevel}:Props) {
  const canEdit = effectiveLevel === "edit";
  const [tab,setTab] = useState<"draft"|"history">("draft");
  const [items,setItems] = useState<Migration[]>([]);
  const [history,setHistory] = useState<MigrationRun[]>([]);
  const [selected,setSelected] = useState<Migration|null>(null);
  const [title,setTitle] = useState("");
  const [description,setDescription] = useState("");
  const [query,setQuery] = useState("");
  const [error,setError] = useState("");
  const [notice,setNotice] = useState("");
  const [busy,setBusy] = useState(false);
  const [plan,setPlan] = useState<unknown>(null);

  async function refresh() {
    setError("");
    try {
      const [drafts, hist] = await Promise.all([
        migrationApi.list(databaseId,"draft"),
        migrationApi.history(databaseId),
      ]);
      setItems(drafts.items); setHistory(hist.items);
    } catch(e) { setError(e instanceof Error ? e.message : "Could not load migrations"); }
  }
  useEffect(()=>{void refresh();},[databaseId]);

  function startNew() {
    setSelected(null); setTitle(""); setDescription(""); setQuery("");
    setNotice(""); setError(""); setPlan(null);
  }
  function select(m:Migration) {
    setSelected(m);setTitle(m.title);setDescription(m.description);setQuery(m.sql);
    setNotice("");setError("");setPlan(null);
  }
  async function save() {
    setBusy(true);setError("");setNotice("");
    try {
      const saved = selected
        ? await migrationApi.update(databaseId,selected.id,{title,description,sql:query,revision:selected.revision})
        : await migrationApi.create(databaseId,{title,description,sql:query});
      setSelected(saved);setTitle(saved.title);setDescription(saved.description);setQuery(saved.sql);
      setNotice("Draft saved.");await refresh();
    } catch(e) { setError(e instanceof Error ? e.message : "Save failed"); }
    finally { setBusy(false); }
  }
  async function execute() {
    if(!selected) return;
    const confirmed = window.confirm(`Execute this SQL on the target database?\n\n${query}`);
    if(!confirmed)return;
    setBusy(true);setError("");setNotice("");
    try {
      const run=await migrationApi.execute(databaseId,selected.id,selected.revision);
      setNotice(run.status==="succeeded" ? "Migration applied successfully." : `Migration status: ${run.status}`);
      await refresh();
    } catch(e) {setError(e instanceof Error ? e.message : "Execution failed");}
    finally {setBusy(false);}
  }
  async function explain(analyze:boolean) {
    if(!selected)return;
    setBusy(true);setError("");setPlan(null);
    try {setPlan(await migrationApi.explain(databaseId,selected.id,analyze));}
    catch(e){setError(e instanceof Error?e.message:"EXPLAIN failed");}
    finally{setBusy(false);}
  }

  return <main className="mx-auto grid max-w-7xl gap-4 p-6 lg:grid-cols-[320px_1fr]">
    <section className="rounded border p-4">
      <h1 className="text-xl font-semibold">Migrations</h1>
      <div className="my-3 flex gap-2">
        <button onClick={()=>setTab("draft")} aria-pressed={tab==="draft"}>Draft</button>
        <button onClick={()=>setTab("history")} aria-pressed={tab==="history"}>History</button>
      </div>
      {tab==="draft" ? <>
        {canEdit && <button className="mb-3 rounded border px-3 py-2" onClick={startNew}>New draft</button>}
        <ul className="space-y-2">{items.map(m=><li key={m.id}>
          <button className="w-full rounded border p-3 text-left" onClick={()=>select(m)}>
            <strong>{m.title}</strong><div className="text-sm opacity-70">Revision {m.revision} · base v{m.baseVersion}</div>
          </button>
        </li>)}</ul>
      </> : <ul className="space-y-2">{history.map(r=><li key={r.id} className="rounded border p-3">
        <strong>Run #{r.id}</strong><div>{r.status} · {new Date(r.startedAt).toLocaleString()}</div>
        {r.errorMessage && <p className="text-red-700">{r.errorMessage}</p>}
      </li>)}</ul>}
    </section>
    {tab==="draft" && <section className="min-w-0 space-y-3 rounded border p-4">
      <label className="block">Title<input className="mt-1 block w-full rounded border p-2" value={title} disabled={!canEdit} onChange={e=>setTitle(e.target.value)}/></label>
      <label className="block">Description<textarea className="mt-1 block w-full rounded border p-2" value={description} disabled={!canEdit} onChange={e=>setDescription(e.target.value)}/></label>
      <div className="overflow-hidden rounded border">
        <CodeMirror value={query} height="360px" extensions={[sql()]} editable={canEdit} onChange={setQuery} basicSetup={{lineNumbers:true}}/>
      </div>
      {error && <p role="alert" className="text-red-700">{error}</p>}
      {notice && <p role="status" className="text-green-700">{notice}</p>}
      <div className="flex flex-wrap gap-2">
        {canEdit && <button disabled={busy} className="rounded border px-3 py-2" onClick={()=>void save()}>Save draft</button>}
        {canEdit && selected && <button disabled={busy} className="rounded border px-3 py-2" onClick={()=>void execute()}>Execute…</button>}
        {canEdit && selected && <button disabled={busy} className="rounded border px-3 py-2" onClick={()=>void explain(false)}>EXPLAIN</button>}
        {canEdit && selected && <button disabled={busy} className="rounded border px-3 py-2" onClick={()=>void explain(true)}>EXPLAIN ANALYZE</button>}
      </div>
      {plan!==null && <section><h2 className="font-semibold">Execution plan</h2><pre className="max-h-96 overflow-auto rounded bg-neutral-100 p-3 text-sm">{JSON.stringify(plan,null,2)}</pre></section>}
      <p className="text-xs opacity-70">Dialect: {databaseKind}. EXPLAIN ANALYZE executes statements; use only on a safe target.</p>
    </section>}
  </main>;
}
