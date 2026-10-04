import { Link } from "../shared/router";
import { useTitle } from "../shared/title";
import { Header } from "./Header";

export function NotFoundPage() {
  useTitle("見つかりません");
  return (
    <>
      <Header />
      <main className="page narrow">
      <h1 className="heading">見つかりません</h1>
      <p>お探しの展示は終了したか、まだ公開されていません。</p>
      <p>
        <Link to="/">トップへ戻る</Link>
      </p>
      </main>
    </>
  );
}
