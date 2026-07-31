require "sqlite3"

# hm.db への接続。スキーマ (schema.sql) は接続のたびに冪等に適用する。
module DB
  BASE_DIR = File.dirname(File.expand_path(__FILE__))
  PATH = ENV["DATABASE_PATH"] || "#{BASE_DIR}/hm.db"

  module_function

  def open
    db = SQLite3::Database.new(PATH)
    db.execute("PRAGMA journal_mode = WAL")
    db.execute("PRAGMA synchronous = NORMAL")
    db.execute("PRAGMA foreign_keys = ON")
    db.execute_batch(File.read("#{BASE_DIR}/schema.sql"))
    db
  end

  # 書き込みスクリプトの共通後処理。WAL を畳んで単一ファイルに戻す。
  def finalize(db)
    db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    db.execute("PRAGMA journal_mode = delete")
  end
end
