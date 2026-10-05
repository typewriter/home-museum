import { useEffect, useRef, useState } from "react";

// 画像は館から少しずつ取ってくるので、まだ無いことがある。/img/{id}/{w} は
// 取得済みなら R2 への 302、未取得なら 202 とプレースホルダを即座に返す
// (spec_image_cache.md §8)。<img> だけでは 202 を見分けられないので、先に
// redirect: "manual" で確かめ、未取得なら status を間隔を伸ばしながら見に行く。

type State =
  | { kind: "idle" }
  | { kind: "ready" }
  | { kind: "pending"; ahead?: number; eta?: number }
  | { kind: "failed" }
  | { kind: "gone" };

export const imageURL = (id: number, width: number) => `/img/${id}/${width}`;

async function probe(id: number, width: number): Promise<State> {
  const r = await fetch(imageURL(id, width), { redirect: "manual" });
  // R2 なら 302 (opaqueredirect)、ローカルの保管先ならアプリが 200 で中継する。
  if (r.type === "opaqueredirect" || r.status === 200) return { kind: "ready" };
  if (r.status === 202) return pollOnce(id, width);
  return { kind: "gone" };
}

async function pollOnce(id: number, width: number): Promise<State> {
  const r = await fetch(imageURL(id, width) + "/status");
  if (!r.ok) return { kind: "gone" };
  const st = (await r.json()) as { state: string; ready: boolean; ahead?: number; eta_sec?: number };
  if (st.ready) return { kind: "ready" };
  if (st.state === "gone") return { kind: "gone" };
  if (st.state === "failed") return { kind: "failed" };
  return { kind: "pending", ahead: st.ahead, eta: st.eta_sec };
}

// preload は展示室の前後の作品用。未取得ならこの問い合わせで取得待ちに積まれる。
export async function preload(id: number, width: number) {
  try {
    if ((await probe(id, width)).kind === "ready") new Image().src = imageURL(id, width);
  } catch {
    /* 先読みの失敗は無視する */
  }
}

interface Props {
  id: number;
  width: 400 | 1600;
  alt: string;
  className?: string;
  eager?: boolean; // 画面に入るのを待たずに読む (展示室の主画像)
}

export function Artwork({ id, width, alt, className, eager }: Props) {
  const [state, setState] = useState<State>({ kind: "idle" });
  const [visible, setVisible] = useState(!!eager);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (visible || !ref.current) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          io.disconnect();
        }
      },
      { rootMargin: "400px" },
    );
    io.observe(ref.current);
    return () => io.disconnect();
  }, [visible]);

  useEffect(() => {
    if (!visible) return;
    let stop = false;
    let timer: number | undefined;
    let delay = 4000;
    const tick = async (first: boolean) => {
      let st: State;
      try {
        st = first ? await probe(id, width) : await pollOnce(id, width);
      } catch {
        st = { kind: "failed" };
      }
      if (stop) return;
      setState(st);
      if (st.kind === "pending") {
        timer = window.setTimeout(() => tick(false), delay);
        delay = Math.min(delay * 1.5, 30000);
      }
    };
    tick(true);
    return () => {
      stop = true;
      window.clearTimeout(timer);
    };
  }, [id, width, visible]);

  if (state.kind === "ready") {
    return <img className={className} src={imageURL(id, width)} alt={alt} decoding="async" />;
  }
  return (
    <div ref={ref} className={`artwork-placeholder ${className ?? ""}`} role="img" aria-label={alt}>
      <span>{message(state)}</span>
    </div>
  );
}

function message(st: State): string {
  switch (st.kind) {
    case "idle":
      return "";
    case "pending":
      if (st.ahead == null) return "取得待ち";
      return `取得待ち — あと ${st.ahead} 枚` + (st.eta ? ` (約 ${Math.max(1, Math.round(st.eta / 60))} 分)` : "");
    case "failed":
      return "取得に失敗しました (あとで再試行します)";
    case "gone":
      return "画像を取得できません";
    default:
      return "";
  }
}
