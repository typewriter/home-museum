import { useEffect, useState, type AnchorHTMLAttributes, type MouseEvent } from "react";

// 経路は自前で持つ。person_key は '/' を含み (cluster:rijksmuseum|https://id...)、
// %2F のまま 1 区切りとして扱う必要がある。ルーターライブラリはパスを先に
// デコードしてから照合するものが多く、そこで区切りが増えてしまう。

export function segments(pathname: string): string[] {
  return pathname
    .split("/")
    .filter((s) => s !== "")
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        return s;
      }
    });
}

export function path(...parts: (string | number)[]): string {
  return "/" + parts.map((p) => encodeURIComponent(String(p))).join("/");
}

const NAVIGATE = "viewer:navigate";

export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (opts.replace) history.replaceState(null, "", to);
  else history.pushState(null, "", to);
  window.dispatchEvent(new Event(NAVIGATE));
}

export interface Location {
  pathname: string;
  search: URLSearchParams;
}

function current(): Location {
  return { pathname: location.pathname, search: new URLSearchParams(location.search) };
}

export function useLocation(): Location {
  const [loc, setLoc] = useState(current);
  useEffect(() => {
    const on = () => setLoc(current());
    window.addEventListener("popstate", on);
    window.addEventListener(NAVIGATE, on);
    return () => {
      window.removeEventListener("popstate", on);
      window.removeEventListener(NAVIGATE, on);
    };
  }, []);
  return loc;
}

type LinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & { to: string; replace?: boolean };

export function Link({ to, replace, onClick, ...rest }: LinkProps) {
  const click = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    navigate(to, { replace });
    window.scrollTo(0, 0);
  };
  return <a href={to} onClick={click} {...rest} />;
}
