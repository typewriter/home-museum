import { api, displayTitle, type Kind } from "../shared/api";
import { Artwork } from "../shared/Artwork";
import { Link, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { Footer, Header, SectionHeading } from "./Header";
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
  if (st.status === "error")
    return (
      <>
        <Header back />
        <p className="container page muted">読み込めませんでした。</p>
      </>
    );
  if (st.status === "loading") return <Header back />;

  const { exhibition: ex, works, per } = st.data;
  const pages = Math.max(1, Math.ceil(ex.total / per));
  const base = path(prefix(kind), ex.key);

  return (
    <>
      <Header back />
      <main>
        <section className="band entrance-band">
        <div className="container entrance">
          <div className="entrance-image">
            {ex.cover_id != null && (
              <Link to={base + "/1"} aria-label="大きく表示する" className="entrance-cover-link">
                <Artwork key={ex.cover_id} id={ex.cover_id} width={1600} alt="" className="entrance-cover" eager />
              </Link>
            )}
          </div>
          <div className="entrance-text">
            <p className="eyebrow">{kind === "collection" ? "Virtual Collection Exhibition" : "Artist"}</p>
            <h1 className="display">{ex.title}</h1>
            {ex.subtitle && <p className="subtitle">{ex.subtitle}</p>}
            {ex.description && <p className="description">{ex.description}</p>}
            <p className="meta">作品 {ex.total} 点</p>
            {ex.total > 0 && (
              <Link to={base + "/1"} className="button">
                大きく表示する
              </Link>
            )}
          </div>
        </div>
        </section>

        <section className="container section page">
          <SectionHeading en="Works">作品</SectionHeading>
          <ol className="catalog" start={(page - 1) * per + 1}>
            {works.map((w, i) => {
              const n = (page - 1) * per + i + 1;
              return (
                <li key={w.id}>
                  <Link to={`${base}/${n}`} className="catalog-item">
                    <Artwork key={w.id} id={w.id} width={400} alt={displayTitle(w)} className="catalog-thumb" />
                    <span className="catalog-no">No. {n}</span>
                    <span className="catalog-title">{displayTitle(w)}</span>
                    <span className="catalog-meta">
                      {[kind === "collection" ? w.artist : "", w.date].filter(Boolean).join("、")}
                    </span>
                  </Link>
                </li>
              );
            })}
          </ol>
          {pages > 1 && (
            <nav className="pager">
              {page > 1 ? <Link to={`${base}?page=${page - 1}`}>← 前へ</Link> : <span />}
              <span className="pager-count">
                {page} / {pages}
              </span>
              {page < pages ? <Link to={`${base}?page=${page + 1}`}>次へ →</Link> : <span />}
            </nav>
          )}
        </section>
      </main>
      <Footer />
    </>
  );
}
