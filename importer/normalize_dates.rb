#!/usr/bin/env ruby

# images → image_dates。制作年を date_start / date_end / date_precision に正規化する。
#
# 実際のデータに基づいてできるだけ正規化されるようなロジックにしてある。
# （逆に言うと決まったルールがあるわけではなく、場当たり的な実装）
#
#   ruby normalize_dates.rb              全件
#   ruby normalize_dates.rb cleveland    ソース指定
#
# LMDB は読まない (loader.rb が images.date_raw_* に生値を落としてある)。
# ルールを直したら何度でも回し直してよい。
#
# BCE は素直な負数で表す (BCE 1391 = -1391)。AIC の date_start = -100
# ("1st century B.C.E.")、Cleveland の -1391 ("c. 1391-1353 BCE")、
# Paris Musées の -4205 が既にこの規約なので、それに合わせる。

require_relative "db"
require_relative "loaders"

RULE_VERSION = "2026-08-01"

# Paris Musées の startPrecision (仏語) → precision
PM_PRECISION = {
  "en" => "exact", "vers" => "circa", "entre" => "range",
  "après" => "after", "apres" => "after", "avant" => "before",
  "début" => "circa", "debut" => "circa", "fin" => "circa",
}.freeze

module DateText
  module_function

  def normalize(text)
    text.to_s
        .gsub(/[‐-―−]/, "-") # en dash / em dash / minus
        .gsub(/\s+/, " ")
        .strip
        .downcase
  end

  def bce?(text)
    text.match?(/\bb\.?\s?c\.?(\s?e\.?)?\b|\bav\.?\s?j\.?-?c\.?/)
  end

  # Rijksmuseum の dcterms:created は "BC"/"BCE" ではなく "-1700" のように
  # 素の負数で BCE を表す (Cleveland/AIC の date_start と同じ規約)。
  # 数値そのものに符号が付いていればそれを信じ、無ければ語彙由来の sign を掛ける
  def signed(str, sign)
    str.start_with?("-") ? str.to_i : sign * str.to_i
  end

  # 末尾2桁の略記 ("1914-19" / "940-44") を開始年の世紀で補完する。
  # 略記は必ず2桁なので、それ以外の桁数はそのまま返す
  # (3桁年 "940-944" を 1844 に化けさせない)
  def complete(start_year, end_digits)
    return end_digits if end_digits.abs >= 100 || start_year.abs < 100
    century = start_year - (start_year % 100)
    candidate = century + end_digits
    candidate < start_year ? candidate + 100 : candidate
  end

  # 自由記述 → [start, end, precision] | nil
  def parse(raw)
    result = parse_text(raw)
    return result if !result

    start_year, end_year, precision = result
    # 館のデータや表記自体が逆転していることがある
    # ("c. 1886 - c. 1866"、"Before 1797 probably 1780's")。
    # 逆転したまま返すと範囲検索が必ず空振りするので入れ替える
    if start_year && end_year && start_year > end_year
      start_year, end_year = end_year, start_year
    end
    [start_year, end_year, precision]
  end

  def parse_text(raw)
    text = normalize(raw)
    return [nil, nil, "unknown"] if text.empty? || text.match?(/\A(n\.?d\.?|unknown|undated|sans date)\z/)

    sign = bce?(text) ? -1 : 1
    # "c." の直後が空白のことがあるので末尾に \b は付けない ("c. 1570")
    circa = text.match?(/\bca?\.|\bcirca\b|\babout\b|\bapprox|\bvers\b|\benviron\b/)

    # Rijksmuseum の dcterms:created には ISO 日付が混じる
    # ("after 1903-04-11 - c. 1905")。月日は保持しないので年だけ残す。
    # 完全形 (YYYY-MM-DD) だけを対象にする: "1892-12" (年-月) と
    # "1914-19" (年範囲の略記) は見分けが付かないので触らない
    text = text.gsub(/(-?\d{3,4})-\d{2}-\d{2}\b/, '\1')

    # "2004-ongoing" のような現在進行形は下限のみ確定
    if text.match?(/\b(ongoing|present)\b/)
      year = text[/(-?\d{1,4})/, 1]
      return year ? [signed(year, sign), nil, "after"] : [nil, nil, "unknown"]
    end

    # 否定形と仏語を before/after に寄せてから一度に判定する
    # ("not after" は before と after の両方にマッチしてしまうため)
    bounded = text.gsub(/\bnot after\b/, "before").gsub(/\bnot before\b/, "after")
                  .gsub(/\bavant\b/, "before").gsub(/\bapr[eè]s\b/, "after")
    has_before = bounded.match?(/\bbefore\b/)
    has_after = bounded.match?(/\bafter\b/)

    if has_before || has_after
      # 境界の年は3〜4桁だけを数える。"in or before 1892-12" の "12" のような
      # 月を上限年と誤読しないため (2桁しか無いときだけ緩める)
      years = bounded.scan(/(?<![\w.])-?\d{3,4}\b/)
      years = bounded.scan(/(?<![\w.])-?\d{1,4}\b/) if years.empty?

      # 両端が書かれていれば range。before/after は「片側が本当に不明」なとき
      # だけに限る (spec_normalization_year.md)。語順や修飾の別は問わない:
      #   "in or after 1818 - in or before 1842" / "in or before 1790 - in or after 1837"
      #   "after 1635 - 1670" / "1601 - before 1606-01-01"
      if years.size >= 2
        return [signed(years[0], sign), signed(years[1], sign), circa ? "circa" : "range"]
      end

      year = years.first
      return nil if !year
      return has_before && !has_after ? [nil, signed(year, sign), "before"]
                                      : [signed(year, sign), nil, "after"]
    end

    # "16th-17th century" / "17th century"
    if (m = text.match(/(\d{1,2})(?:st|nd|rd|th)?\s*(?:-\s*(\d{1,2})(?:st|nd|rd|th)?\s*)?centur(?:y|ies)/))
      first = m[1].to_i
      last = (m[2] || m[1]).to_i
      return sign.negative? ? [-(first * 100), -((last - 1) * 100 + 1), "century"]
                            : [(first - 1) * 100 + 1, last * 100, "century"]
    end

    # "1710s" / "1700s"
    if (m = text.match(/\b(\d{3,4})s\b/))
      start_year = sign * m[1].to_i
      return [start_year, start_year + 9, "decade"]
    end

    # "1780-1785" / "1914-19" / "1630/36" / "1810 or 1818" / "c. 1875 - c. 1949"
    # (終了年側にも "c."/"circa"/"vers" 等が繰り返されることがあるので、
    # マッチ前に取り除いておく。circa 判定自体は元の text で行済み)
    range_text = text.gsub(/\b(?:ca?\.|circa|about|approx\.?|vers|environ)\s*/, "")
    if (m = range_text.match(%r{(?<![\w.])(-?\d{1,4})\s*(?:-|/|or|ou|to)\s*(-?\d{1,4})\b}))
      if m[1].start_with?("-") || m[2].start_with?("-")
        # Rijksmuseum は BCE を "BC/BCE" ではなく素の負数 ("-1700") で表す。
        # 数値自体が符号とゼロ埋めで既に完成しているので世紀補完はしない
        # ("-0800" を complete() に渡すと abs<1000 で "19" 的な略記と誤認する)
        start_year = signed(m[1], sign)
        end_year = signed(m[2], sign)
      else
        start_year = sign * m[1].to_i
        end_year = sign * complete(m[1].to_i, m[2].to_i)
      end
      return [start_year, end_year, circa ? "circa" : "range"]
    end

    # 複数の数値が並ぶテキスト ("July 27, 1865" 等) では日付の日部分より
    # 年(桁数の一番多いもの)を優先する。桁数が同じ場合は最初のものを使う
    if (m = text.scan(/(?<![\w.])-?\d{1,4}\b/)).any?
      chosen = m.max_by { |s| s.delete_prefix("-").length }
      year = signed(chosen, sign)
      return [year, year, circa ? "circa" : "exact"]
    end

    nil
  end
end

# 館が持つ構造化フィールドと自由記述の両方から最終的な値を決める。
def resolve(source, text, raw_start, raw_end, raw_precision)
  parsed = DateText.parse(text)

  # 館の構造化フィールドが無いソースはテキストのパース結果がすべて
  if raw_start.nil? && raw_end.nil?
    return parsed || [nil, nil, "unknown"]
  end

  # start > end はソース側のバグ (Paris Musées で確認済み)。end を捨てる
  raw_end = nil if raw_start && raw_end && raw_start > raw_end

  # 単年テキストと数値フィールドが食い違うケース (Cleveland で約1.9%)。
  # テキストが単なる単年なのに数値がそれと違う範囲を主張していたら数値を信用しない
  # (例: "1970" に earliest=1965/latest=nil が付いている)
  if parsed && parsed[2] == "exact" && parsed[0] == parsed[1]
    year = parsed[0]
    outside = (raw_start && year < raw_start) || (raw_end && year > raw_end)
    return parsed if outside || raw_end.nil?
  end

  precision =
    if source == "parismusees" && raw_precision
      PM_PRECISION[raw_precision.to_s.strip.downcase]
    elsif source == "aic" && raw_precision.to_s.strip.downcase.start_with?("c.")
      "circa"
    end

  # テキストが "n.d." でも館が数値を持っているなら「不明」ではない
  text_precision = parsed && parsed[2]
  text_precision = nil if text_precision == "unknown"

  precision ||= text_precision
  precision ||=
    if raw_start.nil? then "before"
    elsif raw_start == raw_end || raw_end.nil? then "exact"
    else "range"
    end

  # テキスト由来の precision が範囲を主張しているのに数値が単年なら exact に倒す
  precision = "exact" if precision == "range" && raw_start && raw_start == raw_end

  # テキストが "before 1896" でも館が上下限を持っているなら範囲が分かっている。
  # before/after は「片側が本当に不明」なときだけに限る
  if raw_start && raw_end && %w[before after].include?(precision)
    precision = raw_start == raw_end ? "exact" : "range"
  end

  precision = "before" if raw_start.nil?

  # decade と century の区別はテキストからは付かない ("1700s" は Smithsonian では
  # 1700年代の10年、Cleveland では18世紀を指す)。数値の幅で決める
  if raw_start && raw_end && %w[decade century].include?(precision)
    precision = (raw_end - raw_start) <= 15 ? "decade" : "century"
  end

  # 終了年が無い = 開放区間とは限らない。Paris Musées は endYear の充足率が
  # 15%程度で、大半は「その年の作品」を意味する。after は明示されたときだけ
  raw_end = raw_start if raw_end.nil? && raw_start && precision != "after"

  [raw_start, raw_end, precision]
end

if __FILE__ == $0
sources = ARGV
sources.each { |s| abort "未知のソース: #{s} (#{Loaders::SOURCES.keys.join(', ')})" if !Loaders::SOURCES.key?(s) }

db = DB.open
where = sources.empty? ? "" : "and source in (#{sources.map { '?' }.join(', ')})"

stats = Hash.new(0)
last_id = 0

loop do
  rows = db.execute(
    "select id, source, date, date_raw_start, date_raw_end, date_raw_precision " \
    "from images where id > ? #{where} order by id limit 5000",
    [last_id] + sources
  )
  break if rows.empty?

  db.transaction {
    rows.each { |id, source, text, raw_start, raw_end, raw_precision|
      start_year, end_year, precision = resolve(source, text, raw_start, raw_end, raw_precision)
      stats[precision] += 1
      db.execute(
        "insert into image_dates (image_id, date_start, date_end, date_precision, rule_version) " \
        "values (?, ?, ?, ?, ?) " \
        "on conflict(image_id) do update set " \
        "date_start = excluded.date_start, date_end = excluded.date_end, " \
        "date_precision = excluded.date_precision, rule_version = excluded.rule_version",
        [id, start_year, end_year, precision, RULE_VERSION]
      )
      last_id = id
    }
  }
  STDERR.print "\r#{stats.values.sum}件"
end

STDERR.puts "\r#{stats.values.sum}件"
stats.sort_by { |_, v| -v }.each { |precision, count| puts "  #{precision}: #{count}" }

DB.finalize(db)
db.close
end
