import { useState, type FormEvent } from "react";
import { Link, navigate, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { admin, type SourceStat } from "./api";

export function CollectionList() {
  useTitle("管理");
  const list = useFetch(() => admin.collections(), []);

  return (
    <>
      <h1>コレクション</h1>
      <NewCollection />
      {list.status === "ok" && (
        <table className="admin-table">
          <thead>
            <tr>
              <th>題名</th>
              <th>slug</th>
              <th>公開</th>
              <th className="num">作品</th>
              <th>更新</th>
            </tr>
          </thead>
          <tbody>
            {list.data.collections.map((c) => (
              <tr key={c.id}>
                <td>
                  <Link to={path("admin", "c", c.id)}>{c.title}</Link>
                </td>
                <td className="mono">{c.slug}</td>
                <td>{c.published ? <span className="badge on">公開中</span> : <span className="badge">下書き</span>}</td>
                <td className="num">
                  {c.count}
                  {c.unresolved ? <span className="warn"> (未解決 {c.unresolved})</span> : null}
                </td>
                <td className="muted">{c.updated_at.slice(0, 10)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {list.status === "error" && <p className="error">{list.error.message}</p>}

      <h2>画像の取得状況</h2>
      <ImageStats />
    </>
  );
}

function NewCollection() {
  const [title, setTitle] = useState("");
  const [slug, setSlug] = useState("");
  const [err, setErr] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      const { collection } = await admin.create({ title, slug, sort: "manual", published: false });
      navigate(path("admin", "c", collection.id));
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <form className="admin-inline-form" onSubmit={submit}>
      <input placeholder="題名 (例: 浮世絵)" value={title} onChange={(e) => setTitle(e.target.value)} required />
      <input placeholder="slug (例: ukiyoe)" value={slug} onChange={(e) => setSlug(e.target.value)} required />
      <button type="submit">新しく作る</button>
      {err && <span className="error">{err}</span>}
    </form>
  );
}

type FailureState = "failed" | "gone";
const failureLabel: Record<FailureState, string> = { failed: "再試行待ち", gone: "取得不可" };

// ImageStats は館ごとの取得状況。失敗の記録 (再試行待ち・取得不可) は消せる。
// 消した作品は、次に表示されたときに取り直される。
function ImageStats() {
  const [version, setVersion] = useState(0);
  const stats = useFetch(() => admin.stats(), [version]);
  const [msg, setMsg] = useState("");

  const clear = async (source: string, states: FailureState[], count: number) => {
    const where = source || "すべての館";
    const what = states.map((s) => failureLabel[s]).join("・");
    if (!confirm(`${where}の${what} ${count} 件の記録を消します。次に表示されたときに、館へ取りに行き直します。`)) return;
    try {
      const r = await admin.clearFailures(source, states);
      setMsg(`${where}の${what}を ${r.cleared} 件消しました`);
      setVersion((v) => v + 1);
    } catch (e) {
      setMsg((e as Error).message);
    }
  };

  if (stats.status === "error") return <p className="error">{stats.error.message}</p>;
  if (stats.status !== "ok") return null;
  if (!stats.data.enabled) return <p className="muted">画像キャッシュは無効です。</p>;
  const rows: SourceStat[] = stats.data.stats?.by_source ?? [];
  const total = rows.reduce((a, s) => ({ failed: a.failed + s.failed, gone: a.gone + s.gone }), { failed: 0, gone: 0 });

  const cell = (source: string, state: FailureState, count: number) => (
    <td className="num">
      {count}
      {count > 0 && (
        <button className="link" onClick={() => clear(source, [state], count)}>
          クリア
        </button>
      )}
    </td>
  );

  return (
    <>
      <table className="admin-table compact">
        <thead>
          <tr>
            <th>館</th>
            <th className="num">待ち</th>
            <th className="num">取得済み</th>
            <th className="num">再試行待ち</th>
            <th className="num">取得不可</th>
            <th>待ちが捌けるまで</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((s) => (
            <tr key={s.source}>
              <td>{s.source}</td>
              <td className="num">{s.queued}</td>
              <td className="num">{s.ready}</td>
              {cell(s.source, "failed", s.failed)}
              {cell(s.source, "gone", s.gone)}
              <td>{s.eta}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {total.failed + total.gone > 0 && (
        <p>
          <button onClick={() => clear("", ["failed", "gone"], total.failed + total.gone)}>
            すべての館の再試行待ち・取得不可をクリア ({total.failed + total.gone} 件)
          </button>
        </p>
      )}
      {msg && <p className="ok">{msg}</p>}
    </>
  );
}
