#!/usr/bin/env ruby

# 名寄せできなかった作者を LLM に判定させるためのバッチツール。
# サブエージェントへの指示は author_migration_prompt.md、設計は
# spec_normalization_author.md の「名寄せの実行順序と各段階の信頼度(実測)」。
#
#   ruby author_merge_batch.rb status        進捗
#   ruby author_merge_batch.rb next 30       未判定を30件取り出し .author_merge_batch.json へ
#   ruby author_merge_batch.rb append F      判定結果JSON を CSV に追記し DB へ適用
#
# 判定の正本は author_merges.csv。DB 側は派生層なので normalize_person.rb を
# 回し直せば作り直せるが、LLM の判定は作り直せないため CSV に残す。
#
# 取り出す順序は作品点数の降順に固定してある。未統合の作者は45,000人ほどいて
# 全件は到底さばけないが、点数は極端に偏っている (上位は数千点、大半は1点) ので、
# 点数順に処理すれば少ない判定回数で効果が出る。

require "json"
require "set"
require_relative "../db"
require_relative "../author_names"
require_relative "../author_merges"
include AuthorNames

BATCH_FILE = File.join(__dir__, ".author_merge_batch.json")

# 候補集めのブロッキング。下の名前だけの一致 (Jean, Marie …) はノイズにしかならない
# ので、姓 (最後の有意トークン) の一致・包含・編集距離の近さに絞る。
PARTICLES = %w[van de der den von du la le el di da dos das of the and or with
               firma imprimerie maison company co cie fils freres frere pere
               sons son the a
               editeur edituer imprimeur lithographe dessinateur graveur peintre
               photographe sculpteur architecte libraire signataire atelier
               publisher printer engraver painter studio works factory].freeze
MAX_CANDIDATES = 8
MAX_BLOCK = 400

def tokens(key) = key.split.reject { |t| t.size < 4 || PARTICLES.include?(t) }

def lev(a, b, cap)
  return b.size if a.empty?
  prev = (0..b.size).to_a
  a.each_char.with_index { |ca, i|
    cur = [i + 1]
    b.each_char.with_index { |cb, j| cur << [prev[j + 1] + 1, cur[j] + 1, prev[j] + (ca == cb ? 0 : 1)].min }
    return cap + 1 if cur.min > cap
    prev = cur
  }
  prev[b.size]
end

Row = Struct.new(:key, :name, :birth, :death, :images, :merged, :bkey, :sources, :variants)

# 候補集めに使うキー。末尾の括弧を落とす。Paris Musées の
# "Aubert (Imprimeur, lithographe, éditeur)" をそのまま使うと、姓が "éditeur" だと
# 誤認されて「(éditeur) を含む全員」が候補になってしまう。
def blocking_key(name)
  base = (open = trailing_paren_at(name)) ? name[0, open].strip : name
  name_key(base.empty? ? name : base)
end

# sources / name_variants は images との join が要り、全 7万人ぶん引くと数秒かかる。
# 判定と候補探索には不要なので、JSON に書く分だけ fill_details で後から足す。
def load_artists(db)
  merged = {}
  db.execute(<<~SQL).each { |key, flag| merged[key] = flag == 1 }
    select person_key, max(case when match_method in ('none','source_id') then 0 else 1 end)
    from image_artists where person_key is not null group by person_key
  SQL

  db.execute("select person_key, display_name, birth_year, death_year, image_count from artists")
    .map { |key, name, birth, death, images|
      Row.new(key, name, birth, death, images, merged.fetch(key, false), blocking_key(name))
    }
end

# name_raw にカンマが入る (Paris Musées の "姓, 名" 形式) ので、区切りに
# group_concat を使うと壊れる。JSON 配列で受け取る。
def fill_details(db, rows)
  by_key = rows.uniq(&:key).to_h { |row| [row.key, row] }
  by_key.keys.each_slice(300) { |slice|
    placeholders = (["?"] * slice.size).join(",")
    db.execute(<<~SQL, slice).each { |key, sources, variants|
      select a.person_key, json_group_array(distinct i.source), json_group_array(distinct a.name_raw)
      from image_artists a join images i on i.id = a.image_id
      where a.person_key in (#{placeholders}) group by a.person_key
    SQL
      by_key[key].sources = JSON.parse(sources)
      by_key[key].variants = JSON.parse(variants).first(6)
    }
  }
  rows.each { |row| row.sources ||= []; row.variants ||= [] }
end

# 対象1件ごとに全 7万件を舐めると 20件で10秒かかる。姓・語頭・語尾で索引を作り、
# 比較する相手を絞る。
class CandidateIndex
  def initialize(rows)
    @by_token = Hash.new { |h, k| h[k] = [] }
    @by_edge = Hash.new { |h, k| h[k] = [] }

    rows.each { |row|
      next if row.bkey.empty?
      tokens(row.bkey).each { |token| @by_token[token] << row }
      squeezed = row.bkey.delete(" ")
      next if squeezed.size < 5
      @by_edge["h:#{squeezed[0, 4]}"] << row
      @by_edge["t:#{squeezed[-4..]}"] << row
    }
  end

  def for(target)
    return [] if target.bkey.empty?

    found = Set.new
    target_tokens = tokens(target.bkey)

    # 姓 (最後の有意トークン) が一致するもの
    block(@by_token, target_tokens.last) { |c| found << c } if target_tokens.any?

    # 包含関係にあるものは必ずトークンを共有している
    target_tokens.each { |token|
      block(@by_token, token) { |c|
        found << c if [c.bkey.size, target.bkey.size].min >= 5 &&
                      (c.bkey.include?(target.bkey) || target.bkey.include?(c.bkey))
      }
    }

    # 綴り違いは語頭か語尾のどちらかが残っていることが多い
    squeezed = target.bkey.delete(" ")
    if squeezed.size >= 5
      ["h:#{squeezed[0, 4]}", "t:#{squeezed[-4..]}"].each { |key|
        block(@by_edge, key) { |c|
          found << c if (c.bkey.size - target.bkey.size).abs <= 2 && lev(c.bkey, target.bkey, 2) <= 2
        }
      }
    end

    found.delete(target)
    # 判定材料として有用な順: 点数が多い = 統合先として妥当なことが多い
    found.to_a.sort_by { |c| [c.merged ? 0 : 1, -c.images] }.first(MAX_CANDIDATES)
  end

  private

  # 巨大なブロック (ありふれた姓など) は中身が信用できないので丸ごと諦める
  def block(index, key)
    return if key.nil?
    rows = index[key]
    return if rows.size > MAX_BLOCK
    rows.each { |row| yield row }
  end
end

def as_json(row)
  { person_key: row.key, display_name: row.name,
    birth_year: row.birth, death_year: row.death,
    image_count: row.images, sources: row.sources, name_variants: row.variants }
end

# CSV の指示を hm.db に反映する。artists 側は、段階4 でパースした生没年を
# 失わないよう作り直さず、統合されたクラスタの行をまとめる形で更新する。
def apply_to_db(db, all, targets)
  mapping, skipped = AuthorMerges.resolve(all.map(&:key).to_set, targets)
  STDERR.puts "  キーが解決できず読み飛ばし: #{skipped.size}件" if skipped.any?
  return 0 if mapping.empty?

  details = AuthorMerges.details
  known = all.to_h { |r| [r.key, r] }
  moved = 0

  db.transaction {
    mapping.each { |from, into|
      detail = details[from]
      moved += db.get_first_value("select count(*) from image_artists where person_key = ?", [from])
      # 単独だった行だけ理由を llm に書き換える。機械ルールで既にまとまっていた
      # 分の理由は残す (その統合の根拠は今も有効なため)。
      db.execute(
        "update image_artists set person_key = ?, match_method = 'llm', " \
        "match_confidence = ?, match_reason = ? " \
        "where person_key = ? and match_method in ('none','source_id')",
        [into, detail&.dig("confidence"), detail&.dig("reason"), from]
      )
      db.execute("update image_artists set person_key = ? where person_key = ?", [into, from])

      # artists: 吸収された行を消し、統合先に生没年を補う
      source, target = known[from], known[into]
      if source && target
        db.execute("update artists set birth_year = coalesce(birth_year, ?), " \
                   "death_year = coalesce(death_year, ?) where person_key = ?",
                   [source.birth, source.death, into])
      end
      db.execute("delete from artists where person_key = ?", [from])

      # display_name は「役割接頭辞を除いた表記の最頻値」(spec_schema.md §5)。
      # normalize_person.rb を回し直したときと同じ値になるよう数え直す。
      counts = Hash.new(0)
      db.execute(<<~SQL, [into]).each { |src, raw, n| counts[name_core(raw, src)] += n }
        select i.source, a.name_raw, count(*)
        from image_artists a join images i on i.id = a.image_id
        where a.person_key = ? group by 1, 2
      SQL
      display = counts.max_by { |name, n| [n, -name.length] }&.first
      db.execute("update artists set display_name = ? where person_key = ?", [display, into]) if display
    }

    # image_count の数え直しは触ったキーだけにする。全 7万行を書き直すと
    # artists_image_count 索引まで作り直すことになり、統合1件でも重い。
    mapping.values.uniq.each { |key|
      db.execute(<<~SQL, [key, key])
        update artists set image_count = coalesce(
          (select count(distinct a.image_id) from image_artists a where a.person_key = ?), 0)
        where person_key = ?
      SQL
    }
    db.execute("delete from artists where image_count = 0 and person_key in " \
               "(#{(['?'] * mapping.values.uniq.size).join(',')})", mapping.values.uniq)
  }
  moved
end

# ---------------------------------------------------------------- コマンド

command, argument = ARGV
db = DB.open

case command
when "status"
  all = load_artists(db)
  unmerged = all.reject(&:merged)
  decided = AuthorMerges.decided_keys
  todo = unmerged.reject { |r| decided.include?(r.key) }
  merged_rows = AuthorMerges.instructions.size
  puts "artists=#{all.size} 未統合=#{unmerged.size} 判定済=#{decided.size} " \
       "(うち統合=#{merged_rows}) 未判定=#{todo.size} 未判定の作品数=#{todo.sum(&:images)}"

when "next"
  size = (argument || 30).to_i
  all = load_artists(db)
  decided = AuthorMerges.decided_keys
  todo = all.reject(&:merged).reject { |r| decided.include?(r.key) }
            .sort_by { |r| [-r.images, r.key] }.first(size)

  if todo.empty?
    File.write(BATCH_FILE, "[]")
    puts "remaining=0"
  else
    index = CandidateIndex.new(all)
    picked = todo.to_h { |row| [row, index.for(row)] }

    # sources / name_variants は JSON に書く分だけ引く
    fill_details(db, picked.flat_map { |row, cands| [row, *cands] })

    batch = picked.map { |row, cands|
      as_json(row).merge(candidates: cands.map { |c| as_json(c).merge(merged: c.merged) })
    }
    File.write(BATCH_FILE, JSON.pretty_generate(batch))
    remaining = all.count { |r| !r.merged && !decided.include?(r.key) } - todo.size
    puts "wrote=#{todo.size} file=#{BATCH_FILE} remaining=#{remaining}"
  end

when "append"
  abort "使い方: ruby author_merge_batch.rb append <判定結果.json>" if argument.nil?
  abort "ファイルが無い: #{argument}" if !File.exist?(argument)

  results = JSON.parse(File.read(argument))
  abort "配列ではない: #{argument}" if !results.is_a?(Array)

  all = load_artists(db)
  known = all.to_h { |r| [r.key, r] }
  decided = AuthorMerges.decided_keys
  now = Time.now.strftime("%Y-%m-%dT%H:%M:%S%:z")

  rows = []
  results.each { |result|
    key = result["person_key"]
    abort "person_key が無い要素がある" if key.to_s.empty?
    abort "知らない person_key: #{key}" if !known.key?(key)
    abort "判定済みの person_key: #{key}" if decided.include?(key)

    into = result["merge_into"].to_s
    if !into.empty?
      abort "統合先が存在しない: #{key} → #{into}" if !known.key?(into)
      abort "自分自身への統合: #{key}" if into == key
      abort "confidence が無い: #{key}" if !%w[high medium low].include?(result["confidence"])
    end

    rows << {
      "person_key" => key, "merge_into" => into.empty? ? nil : into,
      "confidence" => into.empty? ? nil : result["confidence"],
      "reason" => result["reason"], "display_name" => known[key].name,
      "decided_at" => now
    }
    decided << key
  }

  AuthorMerges.append(rows)
  merged = rows.count { |r| r["merge_into"] }
  puts "appended=#{rows.size} (統合=#{merged} / 名寄せ先なし=#{rows.size - merged})"

  # DB へ差分適用する。normalize_person.rb を回し直しても同じ結果になるが、
  # 15分かかるのでバッチのたびには回さない。
  # 今回追記した行だけを渡す。全行を渡すと、適用済みの行は統合元のキーが消えて
  # いるため毎回「解決できない」として警告が出る。
  applied = apply_to_db(db, all, rows.select { |r| r["merge_into"] })
  puts "applied=#{applied} person_key を書き換え"

  # decided は上で組み立てた集合を使い回す。ここで AuthorMerges.decided_keys を
  # 呼ぶと 4万件のループごとに CSV を読み直すことになる (append が6分かかった原因)。
  puts "remaining=#{all.count { |r| !r.merged && !decided.include?(r.key) }}"

else
  abort <<~USAGE
    使い方:
      ruby author_merge_batch.rb status
      ruby author_merge_batch.rb next [件数]
      ruby author_merge_batch.rb append <判定結果.json>
  USAGE
end

DB.finalize(db)
db.close
