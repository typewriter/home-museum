import { useState } from "react";
import { Link, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { admin } from "./api";
import { WorkPicker } from "./WorkPicker";

// ArtistWorks は 1 人の作者の作品を並べ、1 点ずつコレクションに足す。
export function ArtistWorks({ personKey, page }: { personKey: string; page: number }) {
  const [version, setVersion] = useState(0);
  const st = useFetch(() => admin.artistWorks(personKey, page), [personKey, page, version]);
  useTitle(st.status === "ok" ? `${st.data.exhibition.title} — 管理` : "管理");

  if (st.status === "notfound") return <p>見つかりません。</p>;
  if (st.status === "error") return <p className="error">{st.error.message}</p>;
  if (st.status === "loading") return <p className="muted">読み込み中…</p>;

  const { exhibition: ex, works, per } = st.data;
  const pages = Math.max(1, Math.ceil(ex.total / per));
  const base = path("admin", "a", ex.key);

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

      <WorkPicker works={works} onAdded={() => setVersion((v) => v + 1)} />

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
