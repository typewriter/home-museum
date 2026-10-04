import { useEffect } from "react";

const site = "Uchibi";

// 初回の <title> はサーバーが差し込む (internal/web/spa.go)。画面内の移動ではこちらで変える。
export function useTitle(title?: string) {
  useEffect(() => {
    document.title = title ? `${title} | ${site}` : site;
  }, [title]);
}
