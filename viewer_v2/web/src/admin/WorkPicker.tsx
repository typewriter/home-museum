import { useEffect, useState } from "react";
import { displayTitle } from "../shared/api";
import { Artwork } from "../shared/Artwork";
import { Link, path } from "../shared/router";
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

interface Props {
  works: AdminWork[];
  showArtist?: boolean; // 作者がまちまちな一覧 (作品名の検索) で出す
  onAdded: () => void; // 所属コレクションのバッジを取り直すため
}

// WorkPicker は作品のグリッドと「追加先」の選択。作者の作品一覧と作品名の検索で共有する。
export function WorkPicker({ works, showArtist, onAdded }: Props) {
  const [version, setVersion] = useState(0);
  const cols = useFetch(() => admin.collections(), [version]);
  const [target, setTarget] = useState<number | null>(loadTarget);
  const [err, setErr] = useState("");

  useEffect(() => {
    try {
      if (target) localStorage.setItem(targetKey, String(target));
    } catch {
      /* 保存できなくても選び直せばよい */
    }
  }, [target]);

  const collections: Collection[] = cols.status === "ok" ? cols.data.collections : [];
  const titleOf = new Map(collections.map((c) => [c.id, c.title]));

  const add = async (w: AdminWork) => {
    if (!target) return;
    setErr("");
    try {
      await admin.addWorks(target, [w.source_url]);
      setVersion((v) => v + 1);
      onAdded();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <>
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
              {w.title_ja && w.title && <div className="muted small">{w.title}</div>}
              {showArtist && w.artist && <div className="small">{w.artist}</div>}
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
    </>
  );
}
