#!/usr/bin/env ruby

# 外部で作った訳を hm.db に適用する。CSV 側が真の source of truth で、
# DB 側はいつでも作り直せる。設計は spec_normalization_title.md / spec_schema.md。
#
#   ruby apply_translations.rb titles [SOURCE ...]    titles_ja_<source>.csv  → image_translations
#   ruby apply_translations.rb seed                   館が持つ原語表記        → image_artist_names
#   ruby apply_translations.rb artists [SOURCE ...]   artist_names_ja_<source>.csv → image_artist_names
#   ruby apply_translations.rb stale                  原文が変わった訳を一覧
#
# CSV の形式:
#   titles_ja_<source>.csv       source,source_url,title_original,title_ja,confidence,translated_at
#   artist_names_ja_<source>.csv source,name_raw,name_ja,confidence,translated_at
#
# タイトルは作品ごと (source_url がキー) だが、作者名は生表記ごとに1行しか持たない。
# 同じ表記の image_artists 行すべてに同じ訳を展開する。「同じ文字列には同じ訳を当てる」
# だけで「同じ文字列は同じ人物」とは主張していないので、同姓同名の別人が統合される
# ことはない (DB 側のキーはあくまで image_artist_id)。

require_relative "db"
require_relative "loaders"
require "csv"
require "time"

BASE_DIR = File.dirname(File.expand_path(__FILE__))
LANG = "ja"

CJK = /[\p{Han}\p{Hiragana}\p{Katakana}]/

# "勝川 春章" のように漢字名の間に空白が入ることがあるので詰める
def tidy_cjk(name)
  name.gsub(/(?<=[\p{Han}\p{Hiragana}\p{Katakana}])[[:space:]]+(?=[\p{Han}\p{Hiragana}\p{Katakana}])/, "")
end

def sources_from(args)
  args.each { |s| abort "未知のソース: #{s} (#{Loaders::SOURCES.keys.join(', ')})" if !Loaders::SOURCES.key?(s) }
  args.empty? ? Loaders::SOURCES.keys : args
end

def apply_titles(db, sources)
  sources.each { |name|
    path = "#{BASE_DIR}/with_llms/titles_ja_#{name}.csv"
    if !File.exist?(path)
      STDERR.puts "#{name}: #{File.basename(path)} が無いのでスキップ"
      next
    end

    applied = 0
    missing = 0
    rows = CSV.read(path, headers: true)

    rows.each_slice(1000) { |slice|
      db.transaction {
        slice.each { |row|
          db.execute(
            "insert into image_translations " \
            "(image_id, field, lang, text, confidence, method, source_text, translated_at) " \
            "select id, 'title', ?, ?, ?, 'llm', ?, ? from images where source_url = ? " \
            "on conflict(image_id, field, lang) do update set " \
            "text = excluded.text, confidence = excluded.confidence, method = excluded.method, " \
            "source_text = excluded.source_text, translated_at = excluded.translated_at",
            [LANG, row["title_ja"], row["confidence"], row["title_original"],
             row["translated_at"], row["source_url"]]
          )
          db.changes > 0 ? applied += 1 : missing += 1
        }
      }
    }
    puts "#{name}: title #{applied}件適用" + (missing > 0 ? " (images に無い source_url: #{missing}件)" : "")
  }
end

# 館が原語表記を持っている分を method='source' として先に入れておく。
# Cleveland の creators[].name_in_original_language に漢字名がある。
def seed_artist_names(db)
  now = Time.now.iso8601
  seeded = 0
  skipped = 0
  last_id = 0

  loop do
    rows = db.execute(
      "select id, name_raw, name_original_language from image_artists " \
      "where id > ? and name_original_language is not null order by id limit 5000",
      [last_id]
    )
    break if rows.empty?

    db.transaction {
      rows.each { |id, name_raw, original|
        last_id = id
        # ラテン文字の原語表記 (キリル文字等も含む) は日本語名として使えない
        if !original.match?(CJK)
          skipped += 1
          next
        end
        db.execute(
          "insert into image_artist_names " \
          "(image_artist_id, lang, name, confidence, method, name_original, translated_at) " \
          "values (?, ?, ?, 'high', 'source', ?, ?) " \
          "on conflict(image_artist_id, lang) do update set " \
          "name = excluded.name, confidence = excluded.confidence, method = excluded.method, " \
          "name_original = excluded.name_original, translated_at = excluded.translated_at",
          [id, LANG, tidy_cjk(original), name_raw, now]
        )
        seeded += 1
      }
    }
  end

  puts "seed: #{seeded}件 (CJK以外でスキップ: #{skipped}件)"
end

def apply_artist_names(db, sources)
  sources.each { |name|
    path = "#{BASE_DIR}/artist_names_ja_#{name}.csv"
    if !File.exist?(path)
      STDERR.puts "#{name}: #{File.basename(path)} が無いのでスキップ"
      next
    end

    applied = 0
    missing = 0
    rows = CSV.read(path, headers: true)

    rows.each_slice(500) { |slice|
      db.transaction {
        slice.each { |row|
          # 1つの生表記が複数の image_artists 行に展開される。
          # 館が持つ原語表記 (method='source') は上書きしない。
          db.execute(
            "insert into image_artist_names " \
            "(image_artist_id, lang, name, confidence, method, name_original, translated_at) " \
            "select a.id, ?, ?, ?, 'llm', a.name_raw, ? " \
            "  from image_artists a join images i on i.id = a.image_id " \
            " where i.source = ? and a.name_raw = ? " \
            "on conflict(image_artist_id, lang) do update set " \
            "name = excluded.name, confidence = excluded.confidence, method = excluded.method, " \
            "name_original = excluded.name_original, translated_at = excluded.translated_at " \
            "where image_artist_names.method IS NOT 'source'",
            [LANG, row["name_ja"], row["confidence"], row["translated_at"], name, row["name_raw"]]
          )
          count = db.changes
          count > 0 ? applied += count : missing += 1
        }
      }
    }
    puts "#{name}: 作者名 #{applied}行に適用" + (missing > 0 ? " (該当する image_artists が無い表記: #{missing}件)" : "")
  }
end

def report_stale(db)
  titles = db.execute(
    "select count(*) from image_translations t join images i on i.id = t.image_id " \
    "where t.field = 'title' and t.source_text is not null and t.source_text IS NOT i.title"
  ).first.first
  names = db.execute(
    "select count(*) from image_artist_names n join image_artists a on a.id = n.image_artist_id " \
    "where n.name_original is not null and n.name_original IS NOT a.name_raw"
  ).first.first

  puts "原題が変わったタイトル訳: #{titles}件"
  puts "生表記が変わった作者名訳: #{names}件 (position のずれの可能性)"
end

command, *args = ARGV
db = DB.open

case command
when "titles"  then apply_titles(db, sources_from(args))
when "seed"    then seed_artist_names(db)
when "artists" then apply_artist_names(db, sources_from(args))
when "stale"   then report_stale(db)
else
  abort "usage: ruby apply_translations.rb {titles|seed|artists|stale} [SOURCE ...]"
end

DB.finalize(db)
db.close
