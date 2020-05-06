#!/usr/bin/env ruby

require "leveldb"
require "json"
require "sqlite3"

DATABASE_PATH = ENV['DATABASE_PATH'] || "./hm.db"
IMPORT_PATH   = ENV['IMPORT_PATH'] || ARGV[0]

db = SQLite3::Database.new DATABASE_PATH

# create table
sql = <<-SQL
  CREATE TABLE IF NOT EXISTS images (
    id integer primary key,
    source text,
    category text,
    title text,
    artist text,
    date text,
    medium text,
    origin text,
    dimensions text,
    credit text,
    description text,
    source_url text,
    image_url text
  );
SQL
db.execute(sql)


def aic_loader(path)
  records = []
  db = LevelDB::DB.new path
  db.each { |id, v|
    json = JSON.parse(v)
    next if !json["is_public_domain"]
    next if !json.dig("thumbnail", "url")

    records << {
      source: 'AIC',
      category: json["classification_title"],
      title: json["title"],
      artist: json["artist_display"],
      date: json["date_display"],
      medium: json["medium_display"],
      origin: json["place_of_origin"],
      dimensions: json["dimensions"],
      credit: json["credit_line"],
      description: json["description"],
      source_url: "https://www.artic.edu/artworks/#{json["id"]}",
      image_url: json.dig("thumbnail", "url") + "/full/full/0/default.jpg",
    }
  }
  records
end

# insert or update
records = aic_loader(IMPORT_PATH)

records.each { |record|
  # source_url単位
  sql = <<-SQL
    select 1
      from images
      where source_url = ?
  SQL

  if db.execute(sql, record[:source_url]).length > 0
    # FIXME: update not supported
    puts "exists: #{record[:source_url]}"
    next
  end

  puts "insert: #{record[:source_url]}"
  sql = <<-SQL
    insert into images(source, category, title, artist, date, medium, origin, dimensions, credit, description, source_url, image_url)
      values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  SQL
  db.execute(sql,
             record[:source],
             record[:category],
             record[:title],
             record[:artist],
             record[:date],
             record[:medium],
             record[:origin],
             record[:dimensions],
             record[:credit],
             record[:description],
             record[:source_url],
             record[:image_url]
            )
}
