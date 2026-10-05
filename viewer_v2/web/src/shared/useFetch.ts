import { useEffect, useState } from "react";
import { NotFound } from "./api";

export type Fetched<T> =
  | { status: "loading" }
  | { status: "ok"; data: T }
  | { status: "notfound" }
  | { status: "error"; error: Error };

// useFetch は deps が変わるたびに取り直す。前の取得の結果が後から届いても捨てる。
export function useFetch<T>(load: () => Promise<T>, deps: unknown[]): Fetched<T> {
  const [st, setSt] = useState<Fetched<T>>({ status: "loading" });
  useEffect(() => {
    let live = true;
    setSt((prev) => (prev.status === "ok" ? prev : { status: "loading" }));
    load().then(
      (data) => live && setSt({ status: "ok", data }),
      (error: Error) =>
        live && setSt(error instanceof NotFound ? { status: "notfound" } : { status: "error", error }),
    );
    return () => {
      live = false;
    };
  }, deps);
  return st;
}
