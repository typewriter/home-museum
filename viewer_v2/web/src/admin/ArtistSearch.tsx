import { useEffect, useState } from "react";
import { lifeSpan, type Artist } from "../shared/api";
import { Link, navigate, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { admin } from "./api";

// 管理画面では作品数の閾値を掛けずに全作者から探す。
export function ArtistSearch({ q: initial }: { q: string }) {
  useTitle("作者から選ぶ — 管理");
  const [q, setQ] = useState(initial);
  const [artists, setArtists] = useState<Artist[] | null>(null);

  useEffect(() => {
    const ctl = new AbortController();
    const t = window.setTimeout(() => {
      navigate("/admin/artists" + (q ? "?q=" + encodeURIComponent(q) : ""), { replace: true });
      admin.artists(q.trim(), ctl.signal).then(
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
    <>
      <h1>作者から選ぶ</h1>
      <input
        className="admin-search"
        type="search"
        autoFocus
        placeholder="作者名 (表記・日本語名の部分一致)"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <table className="admin-table">
        <tbody>
          {artists?.map((a) => (
            <tr key={a.person_key}>
              <td>
                <Link to={path("admin", "a", a.person_key)}>{a.display_name}</Link>
                {a.name_ja && <span className="muted"> {a.name_ja}</span>}
              </td>
              <td className="muted">{lifeSpan(a)}</td>
              <td className="num">{a.work_count} 点</td>
              <td className="muted small mono">{a.person_key}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {artists?.length === 0 && <p className="muted">見つかりませんでした。</p>}
    </>
  );
}
