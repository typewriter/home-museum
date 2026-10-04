# LLM による名寄せ判定 (段階7) の保管と適用。
#
# 判定の正本は CSV (author_merges.csv) に置く。hm.db 側は派生層なので
# normalize_person.rb を回し直せば作り直せるが、LLM の判定は作り直せないため。
# titles_ja_<source>.csv と apply_translations.rb の関係と同じ構図。
#
#   with_llms/author_merge_batch.rb append  → CSV に追記し、DB へ差分適用 (速い)
#   normalize_person.rb           → 段階1〜6 の後に CSV を読んで同じ結果を再現
#
# person_key を参照しているので、キーが安定していることが前提になる。典拠ID由来の
# キー (ulan: 等) は外部で安定、cluster: 由来はクラスタの代表メンバーから決定的に
# 導いているので、同じルールで再実行すれば変わらない。ルールを変えて cluster: の
# キーが動いた行は解決できなくなるため、その場合は警告して読み飛ばす。

require "csv"
require "set"
require_relative "author_names"

module AuthorMerges
  # 既定はリポジトリ直下。テストで別の場所を使いたいときだけ環境変数で差し替える。
  PATH = ENV["AUTHOR_MERGES_PATH"] ||
         File.join(File.dirname(File.expand_path(__FILE__)), "author_merges.csv")
  HEADERS = %w[person_key merge_into confidence reason display_name decided_at].freeze

  module_function

  # 読み込みは1プロセス内で1回に留める。呼ぶたびにパースすると、うっかり
  # ループの中で呼んだときに桁違いに遅くなる (実際に append が6分かかった)。
  # 追記したら reset! を呼ぶこと。
  def rows
    @rows ||= File.exist?(PATH) ? CSV.read(PATH, headers: true).map(&:to_h) : []
  end

  def reset! = @rows = nil

  def decided_keys = rows.map { |row| row["person_key"] }.to_set

  def append(new_rows)
    exists = File.exist?(PATH)
    CSV.open(PATH, "a") { |csv|
      csv << HEADERS if !exists
      new_rows.each { |row| csv << HEADERS.map { |h| row[h] } }
    }
    reset!
  end

  # 統合の指示だけを取り出す。merge_into が空の行 (名寄せ先なしと判定したもの)
  # は「判定済み」の記録としてだけ使う。
  def instructions
    rows.reject { |row| row["merge_into"].to_s.empty? }
  end

  # クラスタの代表キーを選ぶ。normalize_person.rb の person_keys と同じ規則:
  # 典拠IDがあればその優先順位で、無ければ cluster: キーの辞書順最小。
  def canonical(keys)
    authorities = keys.reject { |key| key.start_with?("cluster:") }
    return keys.min if authorities.empty?

    authorities.min_by { |key| [AuthorNames::AUTHORITY_RANK.index(key.split(":").first) || 99, key] }
  end

  # 指示を union-find でまとめ、各グループの代表キーを返す。
  #   { 元のkey => 統合先のkey }  (代表キー自身は含めない)
  #
  # `targets` を渡すとその行だけを対象にする。増分適用 (with_llms/author_merge_batch.rb の
  # append) では今回追記した行だけを渡すこと。**適用済みの行は統合元のキーが
  # 消えているため、全行を渡すと「解決できない」として毎回警告が出る**。
  # normalize_person.rb は段階1〜6 から作り直すので全行を渡してよい。
  def resolve(known_keys, targets = instructions)
    parent = {}
    find = lambda { |x|
      parent[x] = x if !parent.key?(x)
      root = x
      root = parent[root] while parent[root] != root
      parent[x] = root
      root
    }

    skipped = []
    targets.each { |row|
      from, into = row["person_key"], row["merge_into"]
      if !known_keys.include?(from) || !known_keys.include?(into)
        skipped << from
        next
      end
      ra, rb = find.(from), find.(into)
      parent[ra] = rb if ra != rb
    }

    mapping = {}
    parent.keys.group_by { |key| find.(key) }.each_value { |group|
      target = canonical(group)
      group.each { |key| mapping[key] = target if key != target }
    }
    [mapping, skipped]
  end

  # 判定の理由を引くための索引。統合先ではなく「動かされる側」のキーで引く。
  def details = instructions.to_h { |row| [row["person_key"], row] }
end
