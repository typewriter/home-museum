import { useEffect, useRef } from "react";
import { api, displayTitle, sourceNames, type Kind, type Work } from "../shared/api";
import { Artwork, preload } from "../shared/Artwork";
import { navigate, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { prefix } from "./Entrance";
import { NotFoundPage } from "./NotFoundPage";

interface Props {
  kind: Kind;
  exKey: string;
  n: number;
}

const catalogPer = 60; // internal/web/api_public.go の catalogPer

// Room は展示室。暗い部屋に 1 作品ずつ掛ける。
export function Room({ kind, exKey, n }: Props) {
  const st = useFetch(() => api.room(kind, exKey, n), [kind, exKey, n]);
  const data = st.status === "ok" ? st.data : undefined;
  useTitle(data ? `${displayTitle(data.room.work)} — ${data.exhibition.title}` : undefined);

  const base = path(prefix(kind), exKey);
  const total = data?.exhibition.total ?? 0;
  // 作品ごとの URL は残すが、矢印での移動は履歴を積まない。戻るで入口に帰れるように。
  const go = (to: number) => {
    if (to >= 1 && to <= total) navigate(`${base}/${to}`, { replace: true });
  };
  const exit = () => navigate(`${base}?page=${Math.ceil(n / catalogPer)}`);

  const goRef = useRef({ go, exit, n });
  goRef.current = { go, exit, n };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const { go, exit, n } = goRef.current;
      if (e.key === "ArrowRight") go(n + 1);
      else if (e.key === "ArrowLeft") go(n - 1);
      else if (e.key === "Escape") exit();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (!data) return;
    if (data.room.next_id != null) preload(data.room.next_id, 1600);
    if (data.room.prev_id != null) preload(data.room.prev_id, 1600);
  }, [data]);

  const touch = useRef<{ x: number; y: number } | null>(null);
  const onTouchStart = (e: React.TouchEvent) => {
    const t = e.touches[0];
    touch.current = { x: t.clientX, y: t.clientY };
  };
  const onTouchEnd = (e: React.TouchEvent) => {
    const start = touch.current;
    touch.current = null;
    if (!start) return;
    const t = e.changedTouches[0];
    const dx = t.clientX - start.x;
    const dy = t.clientY - start.y;
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) go(dx < 0 ? n + 1 : n - 1);
  };

  if (st.status === "notfound") return <NotFoundPage />;

  return (
    <div className="room" onTouchStart={onTouchStart} onTouchEnd={onTouchEnd}>
      <header className="room-bar">
        <button className="room-exit" onClick={exit}>
          ← {data?.exhibition.title ?? ""}
        </button>
        <span className="room-count">{data ? `${n} / ${total}` : ""}</span>
      </header>

      <div className="room-wall">
        {n > 1 && (
          <button className="room-arrow prev" aria-label="前の作品" onClick={() => go(n - 1)}>
            ‹
          </button>
        )}
        {data && (
          <Artwork
            key={data.room.work.id}
            id={data.room.work.id}
            width={1600}
            alt={displayTitle(data.room.work)}
            className="room-artwork"
            eager
          />
        )}
        {data && n < total && (
          <button className="room-arrow next" aria-label="次の作品" onClick={() => go(n + 1)}>
            ›
          </button>
        )}
      </div>

      {data && <Caption work={data.room.work} />}
      {st.status === "error" && <p className="muted">読み込めませんでした。</p>}
    </div>
  );
}

function Caption({ work: w }: { work: Work }) {
  return (
    <section className="caption">
      <h1 className="caption-title">{displayTitle(w)}</h1>
      {w.title_ja && w.title && <p className="caption-original">{w.title}</p>}
      <p className="caption-artist">
        {w.artist}
        {w.date && <span className="caption-date">{w.artist ? "、" : ""}{w.date}</span>}
      </p>
      <dl className="caption-meta">
        {w.medium && (
          <>
            <dt>素材・技法</dt>
            <dd>{w.medium}</dd>
          </>
        )}
        {w.dimensions && (
          <>
            <dt>寸法</dt>
            <dd>{w.dimensions}</dd>
          </>
        )}
        {w.credit && (
          <>
            <dt>所蔵</dt>
            <dd>{w.credit}</dd>
          </>
        )}
      </dl>
      <p className="caption-source">
        <a href={w.source_url} target="_blank" rel="noopener noreferrer">
          {sourceNames[w.source] ?? w.source}で見る ↗
        </a>
      </p>
      {w.description && (
        <details className="caption-description">
          <summary>解説 (所蔵館による)</summary>
          <p>{w.description}</p>
        </details>
      )}
      <p className="caption-hint muted small">← → キー、またはスワイプで移動 / Esc で入口へ</p>
    </section>
  );
}
