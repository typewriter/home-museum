import { Link } from "../shared/router";
import { useTitle } from "../shared/title";
import { Header } from "./Header";

export function NotFoundPage() {
  useTitle("見つかりません");
  return (
    <>
      <Header />
      <main className="container page">
        <p className="eyebrow">Not Found</p>
        <h1 className="display">見つかりません</h1>
        <p className="lead">お探しの展示は終了したか、まだ公開されていません。</p>
        <p>
          <Link to="/" className="text-link">
            トップへ戻る
          </Link>
        </p>
      </main>
    </>
  );
}
