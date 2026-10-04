import { useEffect, useState } from "react";
import { displayTitle } from "../shared/api";
import { Artwork } from "../shared/Artwork";
import { Link, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { admin, type AdminWork, type Collection } from "./api";

const targetKey = "viewer-admin-target";

function loadTarget(): number | null {
  try {
    const v = Number(localStorage.getItem(targetKey));
    return v > 0 ? v : null;
  } catch {
    return null;
  }
}

// ArtistWorks は 1 人の作者の作品を並べ、1 点ずつコレクションに足す。
export function ArtistWorks({ personKey, page }: { personKey: string; page: number }) {
  const [version, setVersion] = useState(0);
  const st = useFetch(() => admin.artistWorks(personKey, page), [personKey, page, version]);
  const cols = useFetch(() => admin.collections(), [version]);
  const [target, setTarget] = useState<number | null>(loadTarget);
  const [err, setErr] = useState("");
  useTitle(st.status === "ok" ? `${st.data.exhibition.title} — 管理` : "管理");

  useEffect(() => {
    try {
      if (target) localStorage.setItem(targetKey, String(target));
    } catch {
      /* 保存できなくても選び直せばよい */
    }
  }, [target]);

  if (st.status === "notfound") return <p>見つかりません。</p>;
  if (st.status === "error") return <p className="error">{st.error.message}</p>;
  if (st.status === "loading") return <p className="muted">読み込み中…</p>;

  const { exhibition: ex, works, per } = st.data;
  const collections: Collection[] = cols.status === "ok" ? cols.data.collections : [];
  const titleOf = new Map(collections.map((c) => [c.id, c.title]));
  const pages = Math.max(1, Math.ceil(ex.total / per));
  const base = path("admin", "a", ex.key);

  const add = async (w: AdminWork) => {
    if (!target) return;
    setErr("");
    try {
      await admin.addWorks(target, [w.source_url]);
      setVersion((v) => v + 1);
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <>
      <p>
        <Link to="/admin/artists">← 作者の検索</Link>
      </p>
      <h1>
        {ex.title} {ex.subtitle && <span className="muted">{ex.subtitle}</span>}
      </h1>
      <p className="muted">
        {ex.description} · {ex.total} 点 · <span className="mono">{ex.key}</span>
      </p>

      <div className="admin-target">
        追加先:
        <select value={target ?? ""} onChange={(e) => setTarget(Number(e.target.value) || null)}>
          <option value="">コレクションを選ぶ</option>
          {collections.map((c) => (
            <option key={c.id} value={c.id}>
              {c.title} ({c.count})
            </option>
          ))}
        </select>
        {target && <Link to={path("admin", "c", target)}>このコレクションを開く</Link>}
        {err && <span className="error">{err}</span>}
      </div>

      <ul className="admin-grid">
        {works.map((w) => {
          const inTarget = target != null && w.collections.includes(target);
          return (
            <li key={w.id} className={inTarget ? "in-target" : ""}>
              <Artwork key={w.id} id={w.id} width={400} alt="" className="admin-thumb" base="/api/admin/img" />
              <div className="admin-grid-title">{displayTitle(w)}</div>
              <div className="muted small">{w.date}</div>
              <div className="badges">
                {w.collections.map((id) => (
                  <span key={id} className="badge">
                    {titleOf.get(id) ?? id}
                  </span>
                ))}
              </div>
              <button disabled={!target || inTarget} onClick={() => add(w)}>
                {inTarget ? "追加済み" : "＋ 追加"}
              </button>
            </li>
          );
        })}
      </ul>

      {pages > 1 && (
        <nav className="pager">
          {page > 1 && <Link to={`${base}?page=${page - 1}`}>← 前</Link>}
          <span className="muted">
            {page} / {pages}
          </span>
          {page < pages && <Link to={`${base}?page=${page + 1}`}>次 →</Link>}
        </nav>
      )}
    </>
  );
}
