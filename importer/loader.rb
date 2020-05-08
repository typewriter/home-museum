#!/usr/bin/env ruby

require "leveldb"
require "json"
require "sqlite3"

DATABASE_PATH = ENV['DATABASE_PATH'] || "./hm.db"
db = SQLite3::Database.new DATABASE_PATH
db.execute("PRAGMA journal_mode = WAL")
db.execute("PRAGMA synchronous = NORMAL")

# create table
sql = <<-SQL
  CREATE TABLE IF NOT EXISTS images (
    id integer primary key,
    source text,
    category text,
    style text,
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

  CREATE INDEX IF NOT EXISTS
    images_style ON images (style);

  CREATE UNIQUE INDEX IF NOT EXISTS
    images_source ON images (source_url);
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
      style: json["style_title"],
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

def met_loader(path)
  records = []
  db = LevelDB::DB.new path
  db.each { |id, v|
    json = JSON.parse(v)

    next if !json["isPublicDomain"]
    next if !json["primaryImage"]

    records << {
      source: 'MET',
      category: json["classification"],
      style: json["department"],
      title: json["title"],
      artist: json["artistDisplayName"],
      date: json["objectDate"],
      medium: json["medium"],
      origin: nil,
      dimensions: json["dimensions"],
      credit: json["creditLine"],
      description: nil,
      source_url: json["objectURL"],
      image_url: json["primaryImage"],
    }
  }
  records
end

def paris_loader(path)
  records = []
  db = LevelDB::DB.new path
  db.each { |id, v|
    json = JSON.parse(v)

    next if !json["fieldVisuelsPrincipals"]&.first&.dig("entity","publicUrl")

    json["fieldVisuelsPrincipals"].select{|e|e.dig("entity","publicUrl") != nil}.each_with_index { |principal, i|
      records << {
        source: 'Paris Musées',
        category: (json["fieldOeuvreTypesObjet"] || []).map{|e|e.dig("entity","name")}.compact.join(','),
        style: (json["fieldOeuvreThemeRepresente"] || []).map{|e|e.dig("entity","name")}.compact.join(','),
        title: json["title"],
        artist: (json["fieldOeuvreAuteurs"] || []).map{|e|e.dig("entity","fieldAuteurAuteur","entity","name")}.compact.join(','),
        date: ((json.dig("fieldDateProduction","startYear") || "").to_s + "-" + (json.dig("fieldDateProduction","endYear") || "").to_s).gsub(/-$/,""),
        medium: (json["fieldMateriauxTechnique"] || []).map{|e|e.dig("entity","name")}.compact.join(','),
        origin: nil,
        dimensions: nil,
        credit: nil,
        description: nil,
        source_url: json["absolutePath"] + "?#{i}",
        image_url: principal.dig("entity","publicUrl"),
      }
    }
  }
  records
end

# insert or update
records = aic_loader(ARGV[0])
records += met_loader(ARGV[1])
records += paris_loader(ARGV[2])

records.each_slice(1000) { |slice_records|
  db.transaction {
    slice_records.each { |record|
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
        insert into images(source, category, style, title, artist, date, medium, origin, dimensions, credit, description, source_url, image_url)
          values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
      SQL
      db.execute(sql,
                 record[:source],
                 record[:category],
                 record[:style],
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
  }
}

db.execute("reindex")
db.execute("vacuum")
