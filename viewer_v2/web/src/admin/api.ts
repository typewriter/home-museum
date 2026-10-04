import { getJSON, type Artist, type Exhibition, type WorkCard } from "../shared/api";

export type Sort = "manual" | "added_desc" | "year_asc" | "year_desc" | "title";

export const sorts: { value: Sort; label: string }[] = [
  { value: "manual", label: "手動 (ドラッグで並べる)" },
  { value: "added_desc", label: "追加の新しい順" },
  { value: "year_asc", label: "制作年の古い順" },
  { value: "year_desc", label: "制作年の新しい順" },
  { value: "title", label: "題名順" },
];

export interface Collection {
  id: number;
  slug: string;
  title: string;
  title_en?: string;
  description?: string;
  cover_url?: string;
  sort: Sort;
  published: boolean;
  created_at: string;
  updated_at: string;
  count: number;
  unresolved?: number;
  cover_id?: number;
}

export type CollectionInput = Pick<
  Collection,
  "slug" | "title" | "title_en" | "description" | "cover_url" | "sort" | "published"
>;

export interface Member {
  source_url: string;
  position: number;
  note?: string;
  added_at: string;
  work?: WorkCard;
}

export interface AdminWork extends WorkCard {
  source_url: string;
  collections: number[];
}

export interface SourceStat {
  source: string;
  queued: number;
  ready: number;
  failed: number;
  gone: number;
  eta: string;
}

// send は書き込み。エラーは API が返した文言をそのまま投げる (画面にそのまま出す)。
async function send<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) {
    let msg = `${r.status}`;
    try {
      msg = ((await r.json()) as { error?: string }).error ?? msg;
    } catch {
      /* JSON でなければ status だけ */
    }
    throw new Error(msg);
  }
  return (r.status === 204 ? undefined : await r.json()) as T;
}

const col = (id: number) => `/api/admin/collections/${id}`;

export const admin = {
  collections: () => getJSON<{ collections: Collection[] }>("/api/admin/collections"),
  collection: (id: number) => getJSON<{ collection: Collection; members: Member[] }>(col(id)),
  create: (c: CollectionInput) => send<{ collection: Collection }>("POST", "/api/admin/collections", c),
  update: (id: number, c: CollectionInput) => send<{ collection: Collection }>("PUT", col(id), c),
  remove: (id: number) => send("DELETE", col(id)),
  addWorks: (id: number, urls: string[]) => send<{ added: number }>("POST", col(id) + "/works", { source_urls: urls }),
  removeWork: (id: number, url: string) => send("DELETE", col(id) + "/works?source_url=" + encodeURIComponent(url)),
  setNote: (id: number, url: string, note: string) => send("PUT", col(id) + "/note", { source_url: url, note }),
  setOrder: (id: number, urls: string[]) => send("PUT", col(id) + "/order", { source_urls: urls }),
  artists: (q: string, signal?: AbortSignal) =>
    getJSON<{ artists: Artist[] }>("/api/admin/artists?q=" + encodeURIComponent(q), { signal }),
  artistWorks: (key: string, page: number) =>
    getJSON<{ exhibition: Exhibition; page: number; per: number; works: AdminWork[] }>(
      "/api/admin/artists/" + encodeURIComponent(key) + "?page=" + page,
    ),
  searchWorks: (q: string, page: number, signal?: AbortSignal) =>
    getJSON<{ page: number; per: number; has_more: boolean; works: AdminWork[] }>(
      "/api/admin/works?q=" + encodeURIComponent(q) + "&page=" + page,
      { signal },
    ),
  stats: () => getJSON<{ enabled: boolean; stats?: { by_source: SourceStat[] | null } }>("/api/admin/stats"),
};
