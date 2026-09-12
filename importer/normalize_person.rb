#!/usr/bin/env ruby

# image_artists の person_key / match_method / match_confidence / match_reason と
# artists テーブルを埋める。設計は spec_normalization_author.md 「名寄せの実行順序と
# 各段階の信頼度(実測)」および「名寄せ結果の記録」。
#
#   ruby normalize_person.rb
#
# 段階1..7 を順に適用する。
#
#   1 典拠ID / 2 名前完全一致→典拠 / 3 同一ソース内の同名別ID
#   4 生没年のパース (統合ではなく検証基盤) / 5 ソース間の同名 / 6 区切りの違い
#   7 LLM の判定 (author_merges.csv)
#
# 段階7 の判定は author_merges.csv が正本なので、このスクリプトを何度回しても
# 失われない。判定を作るのは author_merge_batch.rb (指示は author_migration_prompt.md)。
#
# 段階7以降で保留にしている手法 (first+last / 編集距離) を足すときは、段階6の下に
# もう1つ merge_by を足して match_method の値を増やすだけでよく、スキーマは変わらない。
#
# 統合の判断は非対称にしてある:
#   生没年が一致   → 統合し high      (裏が取れた)
#   生没年が無い   → 統合し medium    (裏が取れていないだけで、否定材料も無い)
#   生没年が不一致 → 統合しない       (別人の可能性がある)
# 「見逃しより誤統合のほうがコストが高い」ためで、不一致は「別人と確定」ではなく
# 「確信が持てないので保留」を意味する。

require "json"
require "set"
require_relative "db"
require_relative "loaders"
require_relative "author_names"
require_relative "author_merges"
include AuthorNames

# ---------------------------------------------------------------- 典拠ID

def authority_ids(json)
  return [] if json.nil? || json.empty? || json == "[]"

  JSON.parse(json).filter_map { |url|
    case url
    when %r{viaf\.org/viaf/(\d+)}                 then "viaf:#{$1}"
    when %r{wikidata\.org/(?:entity|wiki)/(Q\d+)} then "wd:#{$1}"
    when %r{ulan/(\d+)}                           then "ulan:#{$1}"
    when %r{rkd\.nl/artists/(\d+)}                then "rkd:#{$1}"
    end
  } - PLACEHOLDER_AUTHORITY
rescue JSON::ParserError
  []
end

# ---------------------------------------------------------------- 段階4: 生没年のパース

# Smithsonian と AIC は生没年を構造化して持たず、自由記述に埋め込んでいる。
# ここで取り出さないと、この2ソースは検証材料が無く medium 止まりになる。
#
# 生没年**ではない**年が同じ書式で混ざっているのが厄介なところ:
#   "born England, active 1775-1806"    活動期間 (生没年ではない)
#   "active 1842 - 1868"                同上
#   "Baccarat Glassworks … founded 1764" 設立年
#   "French, 19th century"              世紀表記
# これらを拾うと、生没年ゲートが偽の確証を与える側に回る。活動期間を示す語が混じる
# 文字列は丸ごと捨てる (保守的に倒す)。
LIFE_REJECT = /\b(active|fl\.?|flourished|founded|found\.|established|est\.|reign\w*)\b/i

DASHES = "–—‒−‐"
YEAR_MIN = 1000
YEAR_MAX = 2030

def valid_year(value)
  year = value.to_i
  year if year >= YEAR_MIN && year <= YEAR_MAX
end

# 自由記述から [生年, 没年] を取り出す。取れなければ nil を返す。
def parse_life_years(text)
  return [nil, nil] if text.to_s.empty?

  normalized = text.tr(DASHES, "-").gsub(/\(\s*\?*\s*\)/, " ").gsub(/\s+/, " ")
  return [nil, nil] if normalized.match?(LIFE_REJECT)

  # "born Chelsea, VT 1821-died New York City 1915" は両方そろって初めて信用する。
  # "born Cuba, 1856-1936" のように born の直後が地名で、年は範囲側にある例がある
  # ため、片方しか取れないときは先に範囲表現を試す
  born = valid_year(normalized[/\bborn\b[^0-9]{0,40}?(\d{4})/i, 1])
  died = valid_year(normalized[/\bdied\b[^0-9]{0,40}?(\d{4})/i, 1])
  return [born, died] if born && died

  # "1729 - 1810" / "17 Jan 1822 - 21 Mar 1884" / "c. 1485-c. 1568" / "1539 - ?" /
  # "? - 1866" / "1822-after 1875" / "1528/32-1605" (二択表記は前半だけ採る)
  match = normalized.match(%r{
    (?<left>\?|\d{4})(?:/\d{1,4})?
    \s*-\s*
    (?:c\.|ca\.|circa|about|after|before)?\s*
    (?:\d{1,2}\s+[A-Za-z]{3,}\s+)?
    (?<right>\?|\d{4})(?:/\d{1,4})?
  }xi)
  return [born, died] if match.nil?

  birth = match[:left] == "?" ? nil : valid_year(match[:left])
  death = match[:right] == "?" ? nil : valid_year(match[:right])
  return [nil, nil] if birth && death && birth > death

  [birth, death]
end

# AIC は年を name_raw ではなく images.artist (artist_display) に持つ。2つの形がある:
#   "Pierre Nolasque Bergeret (French, 1782-1863)\nprinted by chez Martinet (…)"
#   "Henri de Toulouse-Lautrec\nFrench, 1864-1901"
# どちらも1つの display に複数の人物が並びうるので、エントリの名前と一致した箇所の
# 年だけを採る。一致しなければ何も採らない。
def aic_life_years(display, name_raw)
  return [nil, nil] if display.to_s.empty?

  target = name_key(name_core(name_raw, "aic"))
  return [nil, nil] if target.empty?

  # 形1: "名前 (…)" の並びを全部拾って、名前が一致したものの括弧内を見る
  display.scan(/([^()\n]+?)\s*\(([^()]*)\)/) { |name, inside|
    # "… (…) and Lucas van Doetecum (…)" のように連結詞が頭に付く
    name = name.sub(/\A[\s,;]*(?:and|or|with|&)\s+/i, "")
    next if name_key(strip_role_prefix(name)) != target
    years = parse_life_years(inside)
    return years if years.any?
  }

  # 形2: 1行目が名前だけ、2行目が "国籍, 生没年" の説明行
  lines = display.split("\n").map(&:strip).reject(&:empty?)
  lines.each_with_index { |line, i|
    next if line.include?("(") || name_key(strip_role_prefix(line)) != target
    descriptor = lines[i + 1]
    next if descriptor.nil? || descriptor.include?("(") || descriptor.include?(":")
    next if strip_role_prefix(descriptor) != descriptor # "after ○○" は別人
    return parse_life_years(descriptor)
  }

  [nil, nil]
end

def authority_rank(id)
  AUTHORITY_RANK.index(id.split(":").first) || AUTHORITY_RANK.size
end

# ---------------------------------------------------------------- Union-Find

class UnionFind
  def initialize
    @parent = {}
  end

  # 再帰にすると長い連鎖でスタックを使い切るので反復で書く
  def find(x)
    @parent[x] = x if !@parent.key?(x)
    root = x
    root = @parent[root] while @parent[root] != root
    while @parent[x] != root
      @parent[x], x = root, @parent[x]
    end
    root
  end

  def union(a, b)
    ra, rb = find(a), find(b)
    @parent[ra] = rb if ra != rb
    rb
  end

  def groups
    @parent.keys.group_by { |x| find(x) }
  end
end

# ---------------------------------------------------------------- 人物候補

# 名寄せの最小単位。館内部IDがあればそれ、無ければ正規化した名前で1件にまとめる。
Person = Struct.new(:pid, :source, :key, :authorities, :births, :deaths, :entries, :names, :from_id) do
  def year_pair = [births.min, deaths.min]

  def years? = !births.empty? || !deaths.empty?
end

Match = Struct.new(:method, :confidence, :reason)

# 生没年の一致判定。:verified / :near / :conflict / :unknown を返す。
#
# 2点、当初の設計から変えている。
#
# 1. 片側が複数の年を持つとき、代表値1つを選んで比べると偽の不一致が出る。
#    Smithsonian の Thomas Doughty は館の中で生年が 1793 と 1791 に割れており、
#    最小値を採ると Cleveland の 1793 と食い違って却下されていた。集合として
#    比べ、どれか1つでも合えば一致とみなす。
# 2. 完全一致だけを採ると、同一人物の生没年が館ごとに1〜3年ぶれている分が
#    すべて却下される。display_name が完全一致するクラスタ 848組のうち 402組が
#    ±3年に収まっており、標本22件はすべて同一人物だった (Volaire 1799/1802、
#    Valck 1651/1652、Bargue 1825/1826 等)。名前が完全一致しているという強い
#    根拠があるので、YEAR_TOLERANCE の範囲は一致として扱う。
#    spec の false-merge 危険例 (Hiller 父子29年差、Campbell 26年差、Brown 38年差)
#    はいずれもこの幅では触れない。
YEAR_TOLERANCE = 3

def compare_set(xs, ys)
  return :unknown if xs.empty? || ys.empty?
  return :verified if xs.any? { |x| ys.include?(x) }
  return :near if xs.any? { |x| ys.any? { |y| (x - y).abs <= YEAR_TOLERANCE } }
  :conflict
end

def compare_years(a, b, placeholder_pairs)
  return :unknown if !a.years? || !b.years?
  return :unknown if placeholder_year?(a, placeholder_pairs)
  return :unknown if placeholder_year?(b, placeholder_pairs)

  born = compare_set(a.births, b.births)
  died = compare_set(a.deaths, b.deaths)

  return :conflict if born == :conflict || died == :conflict
  return :verified if born == :verified || died == :verified
  return :near if born == :near || died == :near

  :unknown
end

# 館が埋めた推定値・番兵かどうか。生没年ゲートの根拠に使わない。
def placeholder_year?(person, placeholder_pairs)
  return true if placeholder_pairs.include?([person.source, *person.year_pair])

  # Met は活動期を「ちょうど100年幅」で生没年欄に入れている (1800-1900 等)。
  # コホート検出は完全一致の組しか数えないので、許容幅を入れると 1800-1900 と
  # 1802-1900 のような推定値同士が近傍ですり抜ける。span が厳密に100年のものは
  # 伝記情報とみなさない。
  person.births.any? { |birth| person.deaths.any? { |death| death - birth == 100 } }
end

# 判定 → (confidence, 理由に添える文言)
def verdict_label(verdict)
  case verdict
  when :verified then ["high", "、生没年が一致"]
  when :near     then ["high", "、生没年が±#{YEAR_TOLERANCE}年で一致"]
  else                ["medium", ""]
  end
end

def load_people(db)
  people = {}

  rows = db.execute(<<~SQL)
    select i.source, a.name_raw, a.source_artist_id, a.authority_urls,
           a.birth_year, a.death_year, count(*)
    from image_artists a
    join images i on i.id = a.image_id
    where a.role_bucket is null or a.role_bucket <> 'non_creator'
    group by 1, 2, 3, 4, 5, 6
  SQL

  rows.each { |source, name_raw, source_artist_id, urls, birth, death, count|
    core = name_core(name_raw, source)
    key = name_key(core)
    next if placeholder_key?(key)

    # 館内部IDがあればそれを人物の単位にする。無いソース (Paris/Smithsonian) は
    # 名前そのものが唯一の手がかりなので正規化キーで代用する。
    from_id = !source_artist_id.to_s.empty?
    pid = "#{source}|#{from_id ? source_artist_id : "k:#{key}"}"

    # 段階4: 館が構造化して持たないソースは自由記述から取り出す。
    # Smithsonian は name_raw に埋まっている ("Nilson, German, 1721 - 1788")
    if source == "smithsonian" && birth.nil? && death.nil?
      birth, death = parse_life_years(name_raw)
    end

    person = (people[pid] ||= Person.new(pid, source, key, [], [], [], 0, Hash.new(0), from_id))
    person.authorities.concat(authority_ids(urls))
    person.births << birth if birth && !SENTINEL_YEARS.include?(birth)
    person.deaths << death if death && !SENTINEL_YEARS.include?(death)
    person.entries += count
    person.names[core] += count
  }

  # 人物のキーは「最も多く使われた表記」から決める。最初に読んだ行で決めると、
  # met の "After designs by Alexandre Laemlein" のような外れ値が先に来たときに
  # キーが汚染され、同じ人物の他ソースと一致しなくなる (display_name は最頻値を
  # 採るので表示は正しく見えてしまい、気づきにくい)。
  # 名前をキーにしている人物 (from_id が偽) は key がそのまま pid なので触らない。
  people.each_value { |person|
    person.authorities.uniq!
    next if !person.from_id

    core = person.names.max_by { |name, count| [count, -name.length] }.first
    person.key = name_key(core)
  }
  people
end

# AIC は年が images.artist 側にあるので別に読む。同じ作者でも作品ごとに
# artist_display の書き方が違うことがあるため、最も多く出た値を採用する。
def fill_aic_years(db, people)
  votes = Hash.new { |h, k| h[k] = Hash.new(0) }

  db.execute(<<~SQL).each { |source_artist_id, name_raw, display, count|
    select a.source_artist_id, a.name_raw, i.artist, count(*)
    from image_artists a
    join images i on i.id = a.image_id
    where i.source = 'aic' and i.artist is not null
      and (a.role_bucket is null or a.role_bucket <> 'non_creator')
    group by 1, 2, 3
  SQL
    key = name_key(name_core(name_raw, "aic"))
    next if placeholder_key?(key)

    pid = "aic|#{source_artist_id.to_s.empty? ? "k:#{key}" : source_artist_id}"
    next if !people.key?(pid)

    years = aic_life_years(display, name_raw)
    votes[pid][years] += count if years.any?
  }

  filled = 0
  votes.each { |pid, tally|
    person = people[pid]
    next if person.years? # 館が構造化して持っていればそちらを優先

    birth, death = tally.max_by(&:last).first
    person.births << birth if birth
    person.deaths << death if death
    filled += 1
  }
  filled
end

# 館が埋めた推定値・番兵を洗い出す。同じ (source, birth, death) を大勢が共有して
# いたら伝記情報ではない。
def placeholder_year_pairs(people)
  counts = Hash.new(0)
  people.each_value { |person| counts[[person.source, *person.year_pair]] += 1 if person.years? }
  counts.select { |_, n| n >= YEAR_COHORT_MIN }.keys.to_set
end

# ---------------------------------------------------------------- 段階1..6

def merge_people(people, placeholder_pairs)
  uf = UnionFind.new
  matches = {}
  people.each_key { |pid| uf.find(pid) }

  # 先に決まったルールを後のルールで上書きしない。段階の順序がそのまま優先順位
  record = ->(pid, method, confidence, reason) {
    matches[pid] ||= Match.new(method, confidence, reason)
  }

  # --- 段階1: 典拠ID。同一 ULAN/Wikidata は同一人物とみなしてよい ---
  by_authority = Hash.new { |h, k| h[k] = [] }
  people.each_value { |person| person.authorities.each { |id| by_authority[id] << person.pid } }
  by_authority.each_value { |pids| pids.each_cons(2) { |a, b| uf.union(a, b) } }

  people.each_value { |person|
    next if person.authorities.empty?
    record.(person.pid, "authority_id", "high", "典拠ID: #{person.authorities.sort.join(', ')}")
  }

  # クラスタが持つ典拠IDの集合。段階3以降で「典拠IDによって別人と分かっている2人」を
  # 名前だけで統合してしまわないためのガードに使う。
  cluster_authorities = Hash.new { |h, k| h[k] = Set.new }
  people.each_value { |person|
    cluster_authorities[uf.find(person.pid)].merge(person.authorities)
  }

  # --- 段階2: 典拠ID付き人物の名前辞書を引く ---
  # 1つの名前が複数の典拠クラスタを指す場合 (実測 0.82%) は引かない。
  # "Kitagawa Utamaro" が Met の歌麿と Rijksmuseum の二代目歌麿の両方を指す類。
  dictionary = Hash.new { |h, k| h[k] = Set.new }
  people.each_value { |person|
    dictionary[person.key] << uf.find(person.pid) if !person.authorities.empty?
  }

  people.each_value { |person|
    next if !person.authorities.empty?
    targets = dictionary[person.key]
    next if targets.size != 1

    root = targets.first
    verdict = compare_years(person, people[root], placeholder_pairs)
    next if verdict == :conflict

    new_root = uf.union(person.pid, root)
    cluster_authorities[new_root].merge(cluster_authorities[root])
    confidence, suffix = verdict_label(verdict)
    record.(person.pid, "name_exact_authority", confidence,
            "典拠ID付きの同名人物と一致 (#{person.key})#{suffix}")
  }

  # --- 段階3/5/6: 名前を根拠にした統合 ---
  merge_by = lambda { |group_key, method, label|
    groups = Hash.new { |h, k| h[k] = [] }
    people.each_value { |person|
      key = group_key.(person)
      groups[key] << person if key
    }

    groups.each_value { |members|
      next if members.size < 2 || members.size > MAX_GROUP

      members.combination(2) { |a, b|
        root_a, root_b = uf.find(a.pid), uf.find(b.pid)
        next if root_a == root_b

        # 双方が典拠IDを持つ別クラスタなら、典拠が「別人」と言っているので統合しない
        auth_a, auth_b = cluster_authorities[root_a], cluster_authorities[root_b]
        next if !auth_a.empty? && !auth_b.empty?

        verdict = compare_years(a, b, placeholder_pairs)
        next if verdict == :conflict

        new_root = uf.union(a.pid, b.pid)
        cluster_authorities[new_root] = auth_a | auth_b
        confidence, suffix = verdict_label(verdict)
        [a, b].each { |person| record.(person.pid, method, confidence, "#{label}#{suffix}") }
      }
    }
  }

  # 段階3: 同一ソース内・同一名・別内部ID。Cleveland の John Singer Sargent が
  # id=287402 と id=2559 に分かれている類。半分は館が意図的に分けた別人
  # (Lucas Cranach 父子) なので、生没年が食い違えば統合しない。
  merge_by.(->(p) { p.from_id ? "#{p.source}|#{p.key}" : nil }, "source_id_dup", "同一ソース内の同名別ID")

  # 段階5: ソースをまたいだ名前の完全一致
  merge_by.(->(p) { p.key }, "name_exact", "ソースをまたぐ同名")

  # 段階6: 空白・区切りだけが違う表記 ("Le Roy" / "Leroy")
  merge_by.(->(p) { p.key.delete(" ") }, "punct_variant", "区切りの違いを除いて一致")

  [uf, matches]
end

# 段階7: author_merges.csv の判定を段階1〜6 の結果に重ねる。
# CSV は person_key を参照しているので、いま算出したキーから pid を逆引きする。
def apply_llm_merges(people, uf, keys, matches)
  by_key = Hash.new { |h, k| h[k] = [] }
  keys.each { |pid, key| by_key[key] << pid }

  mapping, skipped = AuthorMerges.resolve(by_key.keys.to_set)
  details = AuthorMerges.details

  mapping.each { |from, into|
    pids = by_key[from]
    target = by_key[into].first
    next if pids.empty? || target.nil?

    detail = details[from]
    pids.each { |pid|
      uf.union(pid, target)
      # 「なぜこの人物がこのクラスタに入ったか」を上書きするのは、段階1〜6 では
      # 統合されていなかった (単独だった) 場合だけにする。機械ルールで既に
      # まとまっていた分の理由は残す。
      current = matches[pid]
      next if current && !%w[source_id none].include?(current.method)

      matches[pid] = Match.new("llm", detail&.dig("confidence"), detail&.dig("reason"))
    }
  }

  [mapping.size, skipped.size]
end

# クラスタごとの person_key を決める。典拠IDがあればその代表、無ければメンバーから
# 決定的に選んだアンカー。連番を使わないので、再実行しても同じキーになる。
def person_keys(people, uf)
  keys = {}

  uf.groups.each_value { |pids|
    members = pids.filter_map { |pid| people[pid] }
    next if members.empty?

    authorities = members.flat_map(&:authorities).uniq
    key = authorities.empty? ? "cluster:#{members.map(&:pid).min}"
                             : authorities.min_by { |id| [authority_rank(id), id] }
    members.each { |person| keys[person.pid] = key }
  }

  keys
end

# ---------------------------------------------------------------- 書き込み

def write_back(db, people, keys, matches)
  # image_artists を id 順に舐めて、行から人物候補を引き直して更新する。
  # 140万行の id を Ruby 側に持たずに済ませるため、キーは都度計算する。
  assigned = 0
  cleared = 0
  last_id = 0

  loop do
    rows = db.execute(<<~SQL, [last_id])
      select a.id, i.source, a.name_raw, a.source_artist_id, a.role_bucket
      from image_artists a
      join images i on i.id = a.image_id
      where a.id > ? order by a.id limit 5000
    SQL
    break if rows.empty?

    db.transaction {
      rows.each { |id, source, name_raw, source_artist_id, role_bucket|
        last_id = id
        key = name_key(name_core(name_raw, source))
        from_id = !source_artist_id.to_s.empty?
        person = people["#{source}|#{from_id ? source_artist_id : "k:#{key}"}"]

        # 除外した行 (プレースホルダー名 / non_creator) は明示的に NULL に戻す。
        # ルールを変えて対象から外れた行が前回の値を持ち続けないようにするため。
        if person.nil? || placeholder_key?(key) || role_bucket == "non_creator"
          db.execute(
            "update image_artists set person_key = null, match_method = null, " \
            "match_confidence = null, match_reason = null where id = ?", [id]
          )
          cleared += 1
          next
        end

        # 統合されなかった人物。館内部IDでまとまっているだけか、完全に単独か
        match = matches[person.pid] || Match.new(person.from_id ? "source_id" : "none", nil, nil)

        db.execute(
          "update image_artists set person_key = ?, match_method = ?, " \
          "match_confidence = ?, match_reason = ? where id = ?",
          [keys[person.pid], match.method, match.confidence, match.reason, id]
        )
        assigned += 1
      }
    }
    STDERR.print "\r書き込み #{assigned + cleared}件"
  end

  STDERR.puts "\r書き込み #{assigned + cleared}件 (person_key あり #{assigned} / なし #{cleared})"
end

def rebuild_artists(db, people, keys)
  # display_name はクラスタ内で最も多く使われた「役割接頭辞を除いた表記」。
  # 素朴に name_raw の最頻を採ると Met の "After Titian (Tiziano Vecellio)" が
  # 代表になってしまう (spec_schema.md §5)。
  names = Hash.new { |h, k| h[k] = Hash.new(0) }
  births = Hash.new { |h, k| h[k] = Hash.new(0) }
  deaths = Hash.new { |h, k| h[k] = Hash.new(0) }

  people.each_value { |person|
    key = keys[person.pid]
    next if key.nil?
    person.names.each { |name, count| names[key][name] += count }
    person.births.each { |year| births[key][year] += 1 }
    person.deaths.each { |year| deaths[key][year] += 1 }
  }

  db.execute("delete from artists")
  db.transaction {
    names.each { |key, candidates|
      # 同数なら短いほうを採る (役割の修飾が残った表記を避ける)
      display = candidates.max_by { |name, count| [count, -name.length] }.first
      db.execute(
        "insert into artists (person_key, display_name, birth_year, death_year, image_count) " \
        "values (?, ?, ?, ?, 0)",
        [key, display, births[key].max_by(&:last)&.first, deaths[key].max_by(&:last)&.first]
      )
    }
  }

  # image_count は person_key を書き終えた後の実データから数える。
  # 相関サブクエリを 9万行ぶん回すと遅いので、集計を一時表に作って join する。
  db.execute("create temp table person_counts as " \
             "select person_key, count(distinct image_id) n from image_artists " \
             "where person_key is not null group by person_key")
  db.execute("update artists set image_count = coalesce(" \
             "(select n from person_counts c where c.person_key = artists.person_key), 0)")
  db.execute("drop table person_counts")
  db.execute("delete from artists where image_count = 0")
end

# ---------------------------------------------------------------- 実行

db = DB.open

STDERR.puts "人物候補を読み込み中..."
people = load_people(db)
STDERR.puts "  #{people.size}人物 / #{people.values.sum(&:entries)}エントリ (プレースホルダーと non_creator を除く)"

filled = fill_aic_years(db, people)
with_years = people.values.count(&:years?)
STDERR.puts "  生没年あり #{with_years}人物 (うち AIC の artist_display から #{filled}人物)"

placeholder_pairs = placeholder_year_pairs(people)
STDERR.puts "  生没年をプレースホルダー扱いにした組: #{placeholder_pairs.size}"

STDERR.puts "名寄せ中..."
uf, matches = merge_people(people, placeholder_pairs)
keys = person_keys(people, uf)
STDERR.puts "  段階1〜6: #{keys.values.uniq.size}人 (統合前 #{people.size}人物)"

# 段階7: LLM の判定 (author_merges.csv)。段階1〜6 の結果に対する追加の統合として
# 適用する。CSV が正本なので、このスクリプトを何度回しても判定は失われない。
applied, skipped = apply_llm_merges(people, uf, keys, matches)
if applied > 0 || skipped > 0
  keys = person_keys(people, uf)
  STDERR.puts "  段階7 (LLM): #{applied}件を適用 → #{keys.values.uniq.size}人" \
              "#{skipped > 0 ? " / キーが解決できず読み飛ばし #{skipped}件" : ''}"
end

write_back(db, people, keys, matches)
rebuild_artists(db, people, keys)

puts "\n手法別のエントリ数:"
db.execute(<<~SQL).each { |method, confidence, count| puts "  #{method.ljust(22)} #{confidence.ljust(7)} #{count}" }
  select match_method, coalesce(match_confidence, '-'), count(*)
  from image_artists where person_key is not null
  group by 1, 2 order by 3 desc
SQL

puts "\nartists: #{db.get_first_value('select count(*) from artists')}人"

DB.finalize(db)
db.close
