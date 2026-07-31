#!/usr/bin/env ruby

# LMDB → hm.db。images と image_artists の「生」の列だけを書く唯一のスクリプト。
#
#   ruby loader.rb                      全ソース
#   ruby loader.rb cleveland aic        指定ソースのみ
#   ruby loader.rb aic=/path/to.lmdb    LMDB のパスを明示
#
# 派生値 (日本語訳・正規化した年・役割バケット) は別テーブルに分けてあるので、
# ここは既存行を上書きしてよい。旧実装の「update not supported」はこのため解消した。
#
# 正規化ルールを直したときに回し直すのは normalize_*.rb / apply_translations.rb で、
# 数十分かかるこのスクリプトを再実行する必要はない。

require_relative "loaders"
require_relative "db"
require "time"

# images のうち loader.rb が書く列 (id/first_seen_at/updated_at 以外)
IMAGE_COLUMNS = %i[
  source source_id source_url image_url title artist date
  date_raw_start date_raw_end date_raw_precision
  category style medium origin dimensions credit description
].freeze

# image_artists のうち loader.rb が書く列 (role_bucket は normalize_artists.rb の担当)
ARTIST_COLUMNS = %i[
  name_raw role_raw qualifier_raw birth_year death_year
  source_artist_id authority_urls name_original_language
].freeze

# 値が1つでも変わったときだけ UPDATE する (updated_at を意味のある値に保つため)。
# where 句の IS NOT は SQLite の NULL 安全な不等価比較。
def upsert_image(db, record, now)
  values = IMAGE_COLUMNS.map { |c| record[c] }
  id = db.get_first_value("select id from images where source_url = ?", [record[:source_url]])

  if !id
    db.execute(
      "insert into images (#{IMAGE_COLUMNS.join(', ')}, first_seen_at, updated_at) " \
      "values (#{(['?'] * IMAGE_COLUMNS.size).join(', ')}, ?, ?)",
      values + [now, now]
    )
    return [db.last_insert_row_id, :inserted]
  end

  # 変化が無ければ何も書かない
  where = IMAGE_COLUMNS.map { |c| "#{c} IS NOT ?" }.join(" OR ")
  db.execute(
    "update images set #{IMAGE_COLUMNS.map { |c| "#{c} = ?" }.join(', ')}, updated_at = ? " \
    "where id = ? and (#{where})",
    values + [now, id] + values
  )
  [id, db.changes > 0 ? :updated : :unchanged]
end

# 作者エントリを (image_id, position) 単位で同期する。
# image_artist_names が image_artists.id にぶら下がるため、delete→insert で
# 採番し直さず、既存行は UPDATE して id を保つ。
def sync_artists(db, image_id, artists)
  existing = db.execute("select position, id from image_artists where image_id = ?", [image_id]).to_h

  artists.each_with_index { |artist, position|
    values = ARTIST_COLUMNS.map { |c| artist[c] }
    id = existing[position]

    if id
      where = ARTIST_COLUMNS.map { |c| "#{c} IS NOT ?" }.join(" OR ")
      db.execute(
        "update image_artists set #{ARTIST_COLUMNS.map { |c| "#{c} = ?" }.join(', ')} " \
        "where id = ? and (#{where})",
        values + [id] + values
      )
    else
      db.execute(
        "insert into image_artists (image_id, position, #{ARTIST_COLUMNS.join(', ')}) " \
        "values (?, ?, #{(['?'] * ARTIST_COLUMNS.size).join(', ')})",
        [image_id, position] + values
      )
    end
  }

  # 作者が減った場合の余りを落とす
  db.execute("delete from image_artists where image_id = ? and position >= ?", [image_id, artists.size])
end

targets = ARGV.map { |arg|
  name, path = arg.split("=", 2)
  abort "未知のソース: #{name} (#{Loaders::SOURCES.keys.join(', ')})" if !Loaders::SOURCES.key?(name)
  [name, path]
}
targets = Loaders::SOURCES.keys.map { |name| [name, nil] } if targets.empty?

db = DB.open

targets.each { |name, path|
  stats = Hash.new(0)
  started = Time.now
  buffer = []

  flush = lambda {
    db.transaction {
      buffer.each { |record|
        now = Time.now.iso8601
        image_id, result = upsert_image(db, record, now)
        stats[result] += 1
        sync_artists(db, image_id, record[:artists] || [])
      }
    }
    buffer.clear
  }

  Loaders.each_record(name, path) { |record|
    next if !record[:source_url] || !record[:image_url]

    buffer << record
    next if buffer.size < 1000

    flush.call
    total = stats.values.sum
    STDERR.print "\r#{name}: #{total}件 (new=#{stats[:inserted]} upd=#{stats[:updated]})"
  }
  flush.call

  total = stats.values.sum
  STDERR.puts "\r#{name}: #{total}件 " \
              "(new=#{stats[:inserted]} upd=#{stats[:updated]} same=#{stats[:unchanged]}) " \
              "#{(Time.now - started).round}s"
}

DB.finalize(db)
db.close
