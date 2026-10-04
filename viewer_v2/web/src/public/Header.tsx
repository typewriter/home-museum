import { Link } from "../shared/router";

export function Header() {
  return (
    <header className="site-header">
      <Link to="/" className="site-name">
        Uchibi
      </Link>
    </header>
  );
}
