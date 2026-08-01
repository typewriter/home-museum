require 'lmdb'
require 'fileutils'

# LevelDB::DB 互換の薄いラッパー ([]=, [], each, has?)
class KVStore
  # mapsize は仮想アドレス空間の予約サイズであり実ディスク消費ではないため、
  # 大きめに確保しておく (未指定だと既定 10MB 程度で MDB_MAP_FULL になる)。
  # Rijksmuseum が 84 万件 / 平均 9KB で 10GB を使い切った実績があるので、
  # ページ・空きページ分の余裕も含めて 64GB を既定にしている。
  MAP_SIZE = (ENV["LMDB_MAP_SIZE"] || 64 * 1024 * 1024 * 1024).to_i

  def initialize(path)
    FileUtils.mkdir_p(path)
    @env = LMDB.new(path, mapsize: MAP_SIZE)
    @db = @env.database
  end

  def [](key)
    @db[key]
  end

  def []=(key, value)
    @db[key] = value
  end

  def has?(key)
    @db.has?(key)
  end

  def count
    @db.stat[:entries]
  end

  def each(&block)
    @db.each(&block)
  end

  def close
    @env.close
  end
end
