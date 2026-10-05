// viewer_v2 serve の JSON API の型 (internal/store) と取得関数。

export type Kind = "collection" | "artist";

export interface WorkCard {
  id: number;
  title?: string;
  title_ja?: string;
  artist?: string;
  date?: string;
}

export interface Work extends WorkCard {
  source: string;
  source_url: string;
  year_start?: number;
  medium?: string;
  dimensions?: string;
  credit?: string;
  description?: string;
}

export interface Exhibition {
  kind: Kind;
  key: string;
  title: string;
  subtitle?: string;
  description?: string;
  cover_id?: number;
  total: number;
}

export interface CatalogPage {
  exhibition: Exhibition;
  page: number;
  per: number;
  works: WorkCard[];
}

export interface RoomResp {
  exhibition: Exhibition;
  room: { n: number; work: Work; prev_id?: number; next_id?: number };
}

export interface CollectionCard {
  slug: string;
  title: string;
  title_en?: string;
  description?: string;
  count: number;
  cover_id?: number;
}

export interface Artist {
  person_key: string;
  display_name: string;
  name_ja?: string;
  birth_year?: number;
  death_year?: number;
  work_count: number;
}

export class NotFound extends Error {}

export async function getJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, init);
  if (r.status === 404) throw new NotFound(path);
  if (!r.ok) throw new Error(`${r.status} ${path}`);
  return r.json() as Promise<T>;
}

const base = (kind: Kind) => (kind === "collection" ? "/api/collections/" : "/api/artists/");

export const api = {
  collections: () => getJSON<{ collections: CollectionCard[] }>("/api/collections"),
  artists: (q: string, signal?: AbortSignal) =>
    getJSON<{ artists: Artist[] }>("/api/artists?q=" + encodeURIComponent(q), { signal }),
  catalog: (kind: Kind, key: string, page: number) =>
    getJSON<CatalogPage>(base(kind) + encodeURIComponent(key) + "?page=" + page),
  room: (kind: Kind, key: string, n: number) =>
    getJSON<RoomResp>(base(kind) + encodeURIComponent(key) + "/works/" + n),
};

export function displayTitle(w: WorkCard): string {
  return w.title_ja || w.title || "無題";
}

export function lifeSpan(a: Artist): string {
  if (a.birth_year == null && a.death_year == null) return "";
  return `${a.birth_year ?? ""}–${a.death_year ?? ""}`;
}

// 館の表示名。キーは importer の Loaders::SOURCES。
export const sourceNames: Record<string, string> = {
  aic: "シカゴ美術館",
  cleveland: "クリーブランド美術館",
  met: "メトロポリタン美術館",
  parismusees: "パリ市立美術館群",
  rijksmuseum: "アムステルダム国立美術館",
  smithsonian: "スミソニアン協会",
  wikimedia: "Wikimedia Commons",
};
