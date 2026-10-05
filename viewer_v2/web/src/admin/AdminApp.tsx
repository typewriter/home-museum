import { Link, segments, useLocation } from "../shared/router";
import { ArtistWorks } from "./ArtistWorks";
import { CollectionEditor } from "./CollectionEditor";
import { CollectionList } from "./CollectionList";
import { ArtistSearch } from "./ArtistSearch";
import { WorkSearch } from "./WorkSearch";

export function AdminApp() {
  const loc = useLocation();
  const seg = segments(loc.pathname).slice(1); // 先頭の "admin" を除く

  let page;
  if (seg.length === 0) page = <CollectionList />;
  else if (seg[0] === "c" && seg.length === 2 && /^\d+$/.test(seg[1])) page = <CollectionEditor id={Number(seg[1])} />;
  else if (seg[0] === "artists" && seg.length === 1) page = <ArtistSearch q={loc.search.get("q") ?? ""} />;
  else if (seg[0] === "works" && seg.length === 1)
    page = <WorkSearch q={loc.search.get("q") ?? ""} page={Math.max(1, Number(loc.search.get("page")) || 1)} />;
  else if (seg[0] === "a" && seg.length === 2)
    page = <ArtistWorks personKey={seg[1]} page={Math.max(1, Number(loc.search.get("page")) || 1)} />;
  else page = <p>見つかりません。</p>;

  return (
    <div className="admin">
      <header className="admin-header">
        <Link to="/admin" className="admin-brand">
          おうちの美術館 管理
        </Link>
        <nav>
          <Link to="/admin">コレクション</Link>
          <Link to="/admin/artists">作者から選ぶ</Link>
          <Link to="/admin/works">作品名から選ぶ</Link>
          <a href="/" target="_blank" rel="noopener">
            公開画面 ↗
          </a>
        </nav>
      </header>
      <main className="admin-main">{page}</main>
    </div>
  );
}
