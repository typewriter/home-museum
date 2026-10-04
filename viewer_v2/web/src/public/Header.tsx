import { Link } from "../shared/router";

export function Header() {
  return (
    <header className="site-header">
      <div className="container">
        <Link to="/" className="site-name">
          おうちの美術館
        </Link>
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
        <p className="footer-note">掲載している作品は、すべてパブリックドメインです。</p>
      </div>
    </footer>
  );
}

// SectionHeading は全ページで同じ見出しの形を繰り返すためのもの (英字の小見出し + 和文)。
export function SectionHeading({ en, children }: { en: string; children: React.ReactNode }) {
  return (
    <h2 className="section-heading">
      <span className="eyebrow">{en}</span>
      {children}
    </h2>
  );
}
