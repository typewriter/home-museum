import { Link } from "../shared/router";
import { useTitle } from "../shared/title";
import { Footer, Header } from "./Header";

export function NotFoundPage() {
  useTitle("見つかりません");
  return (
    <>
      <Header back />
      <main className="container page notfound">
        <p className="eyebrow">Not Found</p>
        <h1 className="display">見つかりません</h1>
        <p className="lead">お探しの展示は終了したか、まだ公開されていません。</p>
        <p>
          <Link to="/" className="text-link">
            トップへ戻る
          </Link>
        </p>
      </main>
      <Footer />
    </>
  );
}
