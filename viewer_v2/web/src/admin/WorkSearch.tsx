import { useEffect, useState } from "react";
import { Link, navigate } from "../shared/router";
import { useTitle } from "../shared/title";
import { admin, type AdminWork } from "./api";
import { WorkPicker } from "./WorkPicker";

type Result = { works: AdminWork[]; hasMore: boolean } | { error: string } | null;

// WorkSearch は作品名 (原題・日本語訳) の部分一致で探して、コレクションに足す。
export function WorkSearch({ q: initial, page }: { q: string; page: number }) {
  useTitle("作品名から選ぶ — 管理");
  const [q, setQ] = useState(initial);
  const [result, setResult] = useState<Result>(null);
  const [loading, setLoading] = useState(false);
  const [version, setVersion] = useState(0);

  useEffect(() => {
    const t = window.setTimeout(() => {
      if (q !== initial) navigate("/admin/works" + (q ? "?q=" + encodeURIComponent(q) : ""), { replace: true });
    }, 400);
    return () => window.clearTimeout(t);
  }, [q, initial]);

  useEffect(() => {
    if (!initial.trim()) {
      setResult(null);
      return;
    }
    const ctl = new AbortController();
    setLoading(true);
    admin.searchWorks(initial, page, ctl.signal).then(
      (r) => {
        setResult({ works: r.works, hasMore: r.has_more });
        setLoading(false);
      },
      (e: Error) => {
        if (e.name !== "AbortError") {
          setResult({ error: e.message });
          setLoading(false);
        }
      },
    );
    return () => ctl.abort();
  }, [initial, page, version]);

  const pageLink = (p: number) => `/admin/works?q=${encodeURIComponent(initial)}&page=${p}`;

  return (
    <>
      <h1>作品名から選ぶ</h1>
      <input
        className="admin-search"
        type="search"
        autoFocus
        placeholder="作品名 (原題・日本語訳の部分一致)"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <p className="hint">180 万件から探すので、結果が出るまで 1〜数秒かかります。</p>
      {loading && <p className="muted">検索中…</p>}
      {result && "error" in result && <p className="error">{result.error}</p>}
      {result && "works" in result && (
        <>
          {result.works.length === 0 && <p className="muted">見つかりませんでした。</p>}
          <WorkPicker works={result.works} showArtist onAdded={() => setVersion((v) => v + 1)} />
          {(page > 1 || result.hasMore) && (
            <nav className="pager">
              {page > 1 && <Link to={pageLink(page - 1)}>← 前</Link>}
              <span className="muted">{page} ページ目</span>
              {result.hasMore && <Link to={pageLink(page + 1)}>次 →</Link>}
            </nav>
          )}
        </>
      )}
    </>
  );
}
