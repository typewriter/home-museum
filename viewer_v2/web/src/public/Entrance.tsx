import { api, displayTitle, type Kind } from "../shared/api";
import { Artwork } from "../shared/Artwork";
import { Link, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { Header } from "./Header";
import { NotFoundPage } from "./NotFoundPage";

export const prefix = (kind: Kind) => (kind === "collection" ? "c" : "a");

interface Props {
  kind: Kind;
  exKey: string;
  page: number;
}

// Entrance は展覧会の入口。カバー、解説、図録 (作品のグリッド)。
export function Entrance({ kind, exKey, page }: Props) {
  const st = useFetch(() => api.catalog(kind, exKey, page), [kind, exKey, page]);
  useTitle(st.status === "ok" ? st.data.exhibition.title : undefined);

  if (st.status === "notfound") return <NotFoundPage />;
  if (st.status === "error") return <p className="page muted">読み込めませんでした。</p>;
  if (st.status === "loading") return <Header />;

  const { exhibition: ex, works, per } = st.data;
  const pages = Math.max(1, Math.ceil(ex.total / per));
  const base = path(prefix(kind), ex.key);

  return (
    <>
      <Header />
      <main className="page">
        <section className="entrance">
          {ex.cover_id != null && (
            <Artwork key={ex.cover_id} id={ex.cover_id} width={1600} alt="" className="entrance-cover" eager />
          )}
          <div className="entrance-text">
            <p className="muted small">{kind === "collection" ? "展覧会" : "作者展"}</p>
            <h1 className="heading">{ex.title}</h1>
            {ex.subtitle && <p className="subtitle">{ex.subtitle}</p>}
            {ex.description && <p className="description">{ex.description}</p>}
            <p className="muted">{ex.total} 点</p>
            {ex.total > 0 && (
              <Link to={base + "/1"} className="enter-button">
                展示室に入る
              </Link>
            )}
          </div>
        </section>

        <section>
          <h2 className="section-title">出品作品</h2>
          <ol className="catalog" start={(page - 1) * per + 1}>
            {works.map((w, i) => {
              const n = (page - 1) * per + i + 1;
              return (
                <li key={w.id}>
                  <Link to={`${base}/${n}`} className="catalog-item">
                    <Artwork key={w.id} id={w.id} width={400} alt={displayTitle(w)} className="catalog-thumb" />
                    <span className="catalog-no">{n}</span>
                    <span className="catalog-title">{displayTitle(w)}</span>
                    {kind === "collection" && w.artist && <span className="muted small">{w.artist}</span>}
                    {w.date && <span className="muted small">{w.date}</span>}
                  </Link>
                </li>
              );
            })}
          </ol>
          {pages > 1 && (
            <nav className="pager">
              {page > 1 && <Link to={`${base}?page=${page - 1}`}>← 前</Link>}
              <span className="muted">
                {page} / {pages}
              </span>
              {page < pages && <Link to={`${base}?page=${page + 1}`}>次 →</Link>}
            </nav>
          )}
        </section>
      </main>
    </>
  );
}
