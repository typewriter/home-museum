import { Link } from "../shared/router";

// back を付けたページ (トップ以外) では、サイト名の下にトップへの戻り道を出す。
export function Header({ back }: { back?: boolean }) {
  return (
    <header className="site-header">
      <div className="container">
        <Link to="/" className="site-name">
          おうちの美術館
        </Link>
        {back && (
          <nav className="breadcrumb" aria-label="パンくずリスト">
            <Link to="/">← トップページへ戻る</Link>
          </nav>
        )}
      </div>
    </header>
  );
}

// Footer は 10% の強調色 (濃いグレー) の帯。ページの終わりを締める。
export function Footer() {
  return (
    <footer className="site-footer">
      <div className="container footer-inner">
        <Link to="/" className="site-name">
          おうちの美術館
        </Link>
        <div className="footer-notes">
          <p>本サービスは、自由に利用できるパブリックドメインの作品を用いています。</p>
          <p>作品名には機械翻訳を用いており、不正確な場合があります。</p>
          <p>
            Developer: たいぷらいた～ （
            <a href="https://www.nyamikan.net/" target="_blank" rel="noopener noreferrer" className="text-link">
              にゃみかん (nyamikan.net)
            </a>
            ）
          </p>
        </div>
      </div>
    </footer>
  );
}

// SectionHeading は全ページで同じ見出しの形を繰り返すためのもの (英字の小見出し + 和文)。
export function SectionHeading({ en, center, children }: { en: string; center?: boolean; children: React.ReactNode }) {
  return (
    <h2 className={center ? "section-heading center" : "section-heading"}>
      <span className="eyebrow">{en}</span>
      {children}
    </h2>
  );
}
