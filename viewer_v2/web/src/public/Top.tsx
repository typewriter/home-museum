import { useEffect, useState } from "react";
import { api, lifeSpan, type Artist } from "../shared/api";
import { Artwork } from "../shared/Artwork";
import { Link, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { Header, SectionHeading } from "./Header";

export function Top() {
  useTitle();
  const cols = useFetch(() => api.collections(), []);

  return (
    <>
      <Header />
      <main className="container page">
        <section className="intro">
          <h1 className="display">
            気軽に楽しむ、<wbr />名画の世界
          </h1>
          <p className="intro-note">※当サイトの作品は、すべてパブリックドメイン（著作権の切れたもの）です。</p>
        </section>

        <section className="section">
          <SectionHeading en="Exhibitions">開催中の展覧会</SectionHeading>
          {cols.status === "ok" && cols.data.collections.length === 0 && (
            <p className="muted">いまは展覧会がありません。作者から作品を探せます。</p>
          )}
          {cols.status === "error" && <p className="muted">読み込めませんでした。</p>}
          <div className="card-grid">
            {cols.status === "ok" &&
              cols.data.collections.map((c) => (
                <Link key={c.slug} to={path("c", c.slug)} className="card">
                  {c.cover_id != null ? (
                    <Artwork key={c.cover_id} id={c.cover_id} width={400} alt="" className="card-image" />
                  ) : (
                    <div className="card-image artwork-placeholder" />
                  )}
                  <div className="card-text">
                    <h3 className="card-title">{c.title}</h3>
                    {c.title_en && <p className="card-sub">{c.title_en}</p>}
                    <p className="card-meta">{c.count} 点</p>
                  </div>
                </Link>
              ))}
          </div>
        </section>

        <ArtistSearch />
      </main>
    </>
  );
}

function ArtistSearch() {
  const [q, setQ] = useState("");
  const [artists, setArtists] = useState<Artist[] | null>(null);

  useEffect(() => {
    const ctl = new AbortController();
    const t = window.setTimeout(() => {
      api.artists(q.trim(), ctl.signal).then(
        (r) => setArtists(r.artists),
        () => {},
      );
    }, 250);
    return () => {
      window.clearTimeout(t);
      ctl.abort();
    };
  }, [q]);

  return (
    <section className="section">
      <SectionHeading en="Artists">作者から探す</SectionHeading>
      <input
        className="search"
        type="search"
        placeholder="作者名で探す (例: Monet、北斎)"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      {!q.trim() && <p className="note">作品の多い作者</p>}
      <ul className="artist-list">
        {artists?.map((a) => (
          <li key={a.person_key}>
            <Link to={path("a", a.person_key)} className="artist-item">
              <span className="artist-name">{a.name_ja || a.display_name}</span>
              <span className="artist-meta">
                {a.name_ja && <>{a.display_name} · </>}
                {lifeSpan(a) && <>{lifeSpan(a)} · </>}
                {a.work_count} 点
              </span>
            </Link>
          </li>
        ))}
      </ul>
      {artists?.length === 0 && <p className="muted">見つかりませんでした。</p>}
    </section>
  );
}
