import {
  closestCenter,
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useEffect, useState, type FormEvent } from "react";
import { displayTitle } from "../shared/api";
import { Artwork } from "../shared/Artwork";
import { Link, navigate, path } from "../shared/router";
import { useTitle } from "../shared/title";
import { useFetch } from "../shared/useFetch";
import { admin, sorts, type Collection, type CollectionInput, type Member } from "./api";

export function CollectionEditor({ id }: { id: number }) {
  const [version, setVersion] = useState(0);
  const reload = () => setVersion((v) => v + 1);
  const st = useFetch(() => admin.collection(id), [id, version]);
  useTitle(st.status === "ok" ? `${st.data.collection.title} — 管理` : "管理");

  if (st.status === "notfound") return <p>見つかりません。</p>;
  if (st.status === "error") return <p className="error">{st.error.message}</p>;
  if (st.status === "loading") return <p className="muted">読み込み中…</p>;

  return (
    <>
      <p>
        <Link to="/admin">← コレクション一覧</Link>
      </p>
      <MetaForm key={st.data.collection.updated_at} collection={st.data.collection} onSaved={reload} />
      <Members collection={st.data.collection} members={st.data.members} onChanged={reload} />
    </>
  );
}

function toInput(c: Collection): CollectionInput {
  return {
    slug: c.slug,
    title: c.title,
    title_en: c.title_en ?? "",
    description: c.description ?? "",
    cover_url: c.cover_url ?? "",
    sort: c.sort,
    published: c.published,
  };
}

function MetaForm({ collection: c, onSaved }: { collection: Collection; onSaved: () => void }) {
  const [form, setForm] = useState(toInput(c));
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const set = <K extends keyof CollectionInput>(k: K, v: CollectionInput[K]) => setForm((f) => ({ ...f, [k]: v }));

  const save = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await admin.update(c.id, form);
      setMsg({ ok: true, text: "保存しました" });
      onSaved();
    } catch (e) {
      setMsg({ ok: false, text: (e as Error).message });
    }
  };

  const del = async () => {
    if (!confirm(`「${c.title}」を消します。メンバー ${c.count} 件も消え、元に戻せません。`)) return;
    await admin.remove(c.id);
    navigate("/admin");
  };

  return (
    <form className="admin-form" onSubmit={save}>
      <h1>{c.title}</h1>
      <label>
        題名
        <input value={form.title} onChange={(e) => set("title", e.target.value)} required />
      </label>
      <label>
        英題
        <input value={form.title_en} onChange={(e) => set("title_en", e.target.value)} />
      </label>
      <label>
        slug
        <input value={form.slug} onChange={(e) => set("slug", e.target.value)} required className="mono" />
        <span className="hint">公開 URL は /c/{form.slug || "…"}。変えると共有済みのリンクが切れます</span>
      </label>
      <label>
        解説
        <textarea rows={4} value={form.description} onChange={(e) => set("description", e.target.value)} />
      </label>
      <label>
        並び順
        <select value={form.sort} onChange={(e) => set("sort", e.target.value as CollectionInput["sort"])}>
          {sorts.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
      </label>
      <label className="checkbox">
        <input type="checkbox" checked={form.published} onChange={(e) => set("published", e.target.checked)} />
        公開する
      </label>
      <p className="hint">
        カバー: {form.cover_url ? <span className="mono">{form.cover_url}</span> : "未設定 (先頭の作品を使う)"}
        {form.cover_url && (
          <button type="button" className="link" onClick={() => set("cover_url", "")}>
            外す
          </button>
        )}
      </p>
      <div className="admin-actions">
        <button type="submit">保存</button>
        {c.published && (
          <a href={path("c", c.slug)} target="_blank" rel="noopener">
            公開画面で見る ↗
          </a>
        )}
        {msg && <span className={msg.ok ? "ok" : "error"}>{msg.text}</span>}
        <button type="button" className="danger" onClick={del}>
          コレクションを消す
        </button>
      </div>
    </form>
  );
}

function Members({
  collection: c,
  members: initial,
  onChanged,
}: {
  collection: Collection;
  members: Member[];
  onChanged: () => void;
}) {
  const [members, setMembers] = useState(initial);
  const [err, setErr] = useState("");
  useEffect(() => setMembers(initial), [initial]);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const manual = c.sort === "manual";

  const onDragEnd = async (e: DragEndEvent) => {
    if (!e.over || e.active.id === e.over.id) return;
    const from = members.findIndex((m) => m.source_url === e.active.id);
    const to = members.findIndex((m) => m.source_url === e.over!.id);
    const next = arrayMove(members, from, to);
    setMembers(next);
    setErr("");
    try {
      await admin.setOrder(c.id, next.map((m) => m.source_url));
    } catch (e) {
      setErr((e as Error).message);
      onChanged();
    }
  };

  const setCover = async (url: string) => {
    try {
      await admin.update(c.id, { ...toInput(c), cover_url: url });
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const remove = async (m: Member) => {
    if (!confirm(`「${m.work ? displayTitle(m.work) : m.source_url}」を外します。`)) return;
    await admin.removeWork(c.id, m.source_url);
    onChanged();
  };

  return (
    <section>
      <h2>
        メンバー <span className="muted">{members.length} 件</span>
      </h2>
      {!manual && <p className="hint">並び順が「手動」のときだけドラッグで並べ替えられます。</p>}
      {members.length === 0 && (
        <p className="muted">
          まだ作品がありません。<Link to="/admin/artists">作者から選ぶ</Link>で追加してください。
        </p>
      )}
      {err && <p className="error">{err}</p>}
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
        <SortableContext items={members.map((m) => m.source_url)} strategy={verticalListSortingStrategy}>
          <ol className="members">
            {members.map((m) => (
              <MemberRow
                key={m.source_url}
                collectionID={c.id}
                member={m}
                draggable={manual}
                isCover={m.source_url === c.cover_url}
                onCover={() => setCover(m.source_url)}
                onRemove={() => remove(m)}
              />
            ))}
          </ol>
        </SortableContext>
      </DndContext>
    </section>
  );
}

interface RowProps {
  collectionID: number;
  member: Member;
  draggable: boolean;
  isCover: boolean;
  onCover: () => void;
  onRemove: () => void;
}

function MemberRow({ collectionID, member: m, draggable, isCover, onCover, onRemove }: RowProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: m.source_url,
    disabled: !draggable,
  });
  const [note, setNote] = useState(m.note ?? "");
  const [saved, setSaved] = useState(m.note ?? "");

  const saveNote = async () => {
    if (note === saved) return;
    await admin.setNote(collectionID, m.source_url, note);
    setSaved(note);
  };

  return (
    <li
      ref={setNodeRef}
      className={`member ${isDragging ? "dragging" : ""}`}
      style={{ transform: CSS.Transform.toString(transform), transition }}
    >
      {draggable && (
        <button className="handle" aria-label="ドラッグして並べ替え" {...attributes} {...listeners}>
          ⠿
        </button>
      )}
      {m.work ? (
        <Artwork key={m.work.id} id={m.work.id} width={400} alt="" className="member-thumb" base="/api/admin/img" />
      ) : (
        <div className="member-thumb artwork-placeholder">未解決</div>
      )}
      <div className="member-body">
        <div className="member-title">
          {m.work ? displayTitle(m.work) : <span className="warn">works に見つかりません</span>}
          {isCover && <span className="badge on">カバー</span>}
        </div>
        <div className="muted small">
          {m.work?.artist} {m.work?.date}
        </div>
        <div className="muted small mono">{m.source_url}</div>
        <input
          className="note"
          placeholder="覚書 (なぜ入れたか)"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          onBlur={saveNote}
        />
      </div>
      <div className="member-actions">
        {m.work && !isCover && (
          <button className="link" onClick={onCover}>
            カバーにする
          </button>
        )}
        <button className="link danger" onClick={onRemove}>
          外す
        </button>
      </div>
    </li>
  );
}
