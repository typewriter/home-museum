# 日本語タイトルCSV (docs/spec_normalization.md) を分割して埋めるためのバッチ管理ツール。
#
#   ruby title_translation_batch.rb SOURCE status       進捗を表示
#   ruby title_translation_batch.rb SOURCE next [N]     未翻訳の先頭N件を batch.json に書き出す (既定20)
#   ruby title_translation_batch.rb SOURCE append FILE  翻訳結果JSONをCSVへ追記
#   ruby title_translation_batch.rb SOURCE scan         対象一覧キャッシュを作り直す
#   ruby title_translation_batch.rb sources             扱えるSOURCE名を一覧表示
#
# SOURCE は Loaders::SOURCES のキー。入力 LMDB も出力 CSV (titles_ja_<SOURCE>.csv) も
# これで決まる。
#
# 翻訳結果JSONの形式:
#   [{"source_url": "...", "title_ja": "...", "confidence": "high|medium|low"}, ...]
#
# 進捗の唯一の状態はCSVそのもの (source_url の有無) なので、中断しても再開できる。

require 'json'
require 'csv'
require 'time'
require_relative '../loaders'

class TitleTranslationBatch
  BASE_DIR = File.dirname(File.expand_path(__FILE__))
  HEADER = %w[source source_url title_original title_ja confidence translated_at].freeze
  CONFIDENCES = %w[high medium low].freeze
  DEFAULT_SIZE = 20

  # LLM に渡す文脈。images の共通スキーマのうち訳語決定に効くものだけを抜く。
  FIELDS = %i[source_url title artist date category style medium origin].freeze

  attr_reader :name, :csv_path, :batch_path, :targets_path

  def initialize(name)
    @name = name
    @label = Loaders.label(name) # 未知の名前はここで KeyError
    @csv_path = "#{BASE_DIR}/titles_ja_#{name}.csv"
    @batch_path = "#{BASE_DIR}/.title_translation_batch_#{name}.json"
    @targets_path = "#{BASE_DIR}/.title_translation_targets_#{name}.jsonl"
    @count_path = "#{BASE_DIR}/.title_translation_targets_#{name}.count"
  end

  # 対象レコードを JSONL にキャッシュする。Rijksmuseum は全走査に数十分かかるので、
  # next/append のたびには走査しない。クローラーを再実行して母数が増えたら作り直す。
  def scan
    # 件数は走査「前」に採る。走査中にクローラーが書き足した分は次回 warn_if_stale
    # で検出させたいので、多め (走査後の値) に記録してはいけない。
    entries = lmdb_entries

    tmp = "#{@targets_path}.tmp"
    seen = {}
    count = 0

    File.open(tmp, 'w') { |f|
      Loaders.each_record(@name) { |record|
        url = record[:source_url]
        next if !url || seen[url]
        seen[url] = true

        # 原題の無いレコードは除く (docs/spec_normalization.md)
        next if record[:title].to_s.strip.empty?

        f.puts JSON.generate(FIELDS.to_h { |k| [k, record[k]] })
        count += 1
      }
    }
    File.rename(tmp, @targets_path)
    File.write(@count_path, entries)
    count
  end

  # 対象レコードを1件ずつ yield する (キャッシュが無ければ作る)。
  def each_target
    scan if !File.exist?(@targets_path)
    warn_if_stale
    File.foreach(@targets_path) { |line| yield JSON.parse(line) }
  end

  # クローラーがまだ回っているソース (Paris Musées 等) では母数が増えるので、
  # 古いキャッシュのまま「全件完了」にならないよう scan 時点との差を知らせる。
  def warn_if_stale
    return if !File.exist?(@count_path)
    scanned = File.read(@count_path).to_i
    current = lmdb_entries
    return if scanned == current

    STDERR.puts "警告: #{@name}.lmdb の件数が scan 時点から変化している " \
                "(#{scanned} → #{current})。#{@name} scan で対象一覧を作り直すこと"
  end

  # 同一プロセスで LMDB env を二重に開かないよう、数えたらすぐ閉じる
  # (開きっぱなしだと GC 時に "closing environment with open transactions" が出る)。
  def lmdb_entries
    store = KVStore.new(Loaders.lmdb_path(@name))
    store.count
  ensure
    store&.close
  end

  # CSV.read で全列をパースすると 12 万行で 7 秒かかる。要るのは行頭の
  # 「ラベル,URL,」だけなので正規表現で拾う (原題の改行による継続行はラベルで
  # 始まらないので拾われない)。next/append は後で status も呼ぶので一度だけ読む。
  def translated_urls
    @translated_urls ||= begin
      prefix = /\A#{Regexp.escape(CSV.generate_line([@label], row_sep: ''))},([^,"\r\n]+),/
      urls = {}
      if File.exist?(@csv_path)
        File.foreach(@csv_path) { |line| (m = prefix.match(line)) and urls[m[1]] = true }
      end
      urls
    end
  end

  def status
    done = translated_urls
    total = 0
    remaining = 0
    each_target { |r|
      total += 1
      remaining += 1 if !done[r["source_url"]]
    }
    { total: total, done: total - remaining, remaining: remaining }
  end

  def next_batch(size)
    done = translated_urls
    batch = []
    each_target { |r|
      next if done[r["source_url"]]
      batch << r
      break if batch.size >= size
    }
    File.write(@batch_path, JSON.pretty_generate(batch))
    batch
  end

  def append(path)
    translations = JSON.parse(File.read(path))

    # 突き合わせに要るのは翻訳結果に載っている source_url だけなので、
    # キャッシュを流しながら該当行だけ拾う (全件をメモリに載せない)。
    wanted = translations.to_h { |t| [t["source_url"], nil] }
    each_target { |r| wanted[r["source_url"]] = r if wanted.key?(r["source_url"]) }

    done = translated_urls
    now = Time.now.iso8601

    rows = translations.map { |t|
      url = t["source_url"]
      record = wanted[url] or raise "未知の source_url: #{url}"
      raise "翻訳済みの source_url: #{url}" if done[url]
      raise "title_ja が空: #{url}" if t["title_ja"].to_s.strip.empty?
      raise "不正な confidence: #{t["confidence"]} (#{url})" if !CONFIDENCES.include?(t["confidence"])

      # title_original は「翻訳時点の原題のスナップショット」なのでLMDBの現在値を採る
      [@label, url, record["title"], t["title_ja"], t["confidence"], now]
    }

    exists = File.exist?(@csv_path)
    CSV.open(@csv_path, 'a') { |csv|
      csv << HEADER if !exists
      rows.each { |row| csv << row }
    }
    rows.each { |row| done[row[1]] = true } # 後続の status にも反映させる
    rows.size
  end
end

USAGE = <<~TEXT
  usage: ruby title_translation_batch.rb SOURCE {status|next [N]|append FILE|scan}
         ruby title_translation_batch.rb sources
TEXT

if ARGV[0] == 'sources'
  Loaders::SOURCES.each { |name, source| puts "#{name}\t#{source[:label]}" }
  exit
end

name, command, arg = ARGV
abort USAGE if !name || !command
abort "未知の SOURCE: #{name} (sources で一覧表示)" if !Loaders::SOURCES.key?(name)

batch = TitleTranslationBatch.new(name)

case command
when 'scan'
  count = batch.scan
  puts "scanned=#{count}件 -> #{batch.targets_path}"
when 'status'
  s = batch.status
  puts "total=#{s[:total]} done=#{s[:done]} remaining=#{s[:remaining]}"
when 'next'
  size = (arg || TitleTranslationBatch::DEFAULT_SIZE).to_i
  records = batch.next_batch(size)
  s = batch.status
  puts "batch=#{records.size}件 -> #{batch.batch_path}"
  puts "total=#{s[:total]} done=#{s[:done]} remaining=#{s[:remaining]}"
when 'append'
  abort "usage: ruby title_translation_batch.rb #{name} append FILE" if !arg
  n = batch.append(arg)
  s = batch.status
  puts "appended=#{n}件"
  puts "total=#{s[:total]} done=#{s[:done]} remaining=#{s[:remaining]}"
else
  abort USAGE
end
