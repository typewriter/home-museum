import { useState, type FormEvent } from "react";
import { Link, navigate, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { admin } from "./api";

export function CollectionList() {
  useTitle("管理");
  const list = useFetch(() => admin.collections(), []);
  const stats = useFetch(() => admin.stats(), []);

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
      {stats.status === "ok" && !stats.data.enabled && <p className="muted">画像キャッシュは無効です。</p>}
      {stats.status === "ok" && stats.data.stats && (
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
            {(stats.data.stats.by_source ?? []).map((s) => (
              <tr key={s.source}>
                <td>{s.source}</td>
                <td className="num">{s.queued}</td>
                <td className="num">{s.ready}</td>
                <td className="num">{s.failed}</td>
                <td className="num">{s.gone}</td>
                <td>{s.eta}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
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
