import type { Kind } from "../shared/api";
import { segments, useLocation } from "../shared/router";
import { Entrance } from "./Entrance";
import { NotFoundPage } from "./NotFoundPage";
import { Room } from "./Room";
import { Top } from "./Top";

const kinds: Record<string, Kind> = { c: "collection", a: "artist" };

export function App() {
  const loc = useLocation();
  const seg = segments(loc.pathname);

  if (seg.length === 0) return <Top />;
  const kind = kinds[seg[0]];
  if (kind && seg.length === 2) {
    const page = Math.max(1, Number(loc.search.get("page")) || 1);
    return <Entrance kind={kind} exKey={seg[1]} page={page} />;
  }
  if (kind && seg.length === 3 && /^\d+$/.test(seg[2])) {
    return <Room kind={kind} exKey={seg[1]} n={Number(seg[2])} />;
  }
  return <NotFoundPage />;
}
