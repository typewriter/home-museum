require "sqlite3"

# hm.db への接続。スキーマ (schema.sql) は接続のたびに冪等に適用する。
module DB
  BASE_DIR = File.dirname(File.expand_path(__FILE__))
  PATH = ENV["DATABASE_PATH"] || "#{BASE_DIR}/hm.db"

  module_function

  # schema.sql の CREATE TABLE IF NOT EXISTS は既存テーブルに列を足さない。
  # 派生列は後から増えるので、不足分だけ ALTER する (ALTER 自体は冪等でないため)。
  ADDED_COLUMNS = {
    "image_artists" => {
      "person_key" => "TEXT", "match_method" => "TEXT",
      "match_confidence" => "TEXT", "match_reason" => "TEXT"
    }
  }.freeze

  def open
    db = SQLite3::Database.new(PATH)
    db.execute("PRAGMA journal_mode = WAL")
    db.execute("PRAGMA synchronous = NORMAL")
    db.execute("PRAGMA foreign_keys = ON")
    # schema.sql より先に流す。schema.sql には新しい列を参照する CREATE INDEX が
    # あるので、既存DBでは列を足してからでないと通らない。
    migrate(db)
    db.execute_batch(File.read("#{BASE_DIR}/schema.sql"))
    db
  end

  def migrate(db)
    ADDED_COLUMNS.each { |table, columns|
      existing = db.execute("pragma table_info(#{table})").map { |row| row[1] }
      next if existing.empty? # テーブル自体が無い = 新規DB。schema.sql が作る

      columns.each { |name, type|
        db.execute("alter table #{table} add column #{name} #{type}") if !existing.include?(name)
      }
    }
  end

  # 書き込みスクリプトの共通後処理。WAL を畳んで単一ファイルに戻す。
  def finalize(db)
    db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    db.execute("PRAGMA journal_mode = delete")
  end
end
