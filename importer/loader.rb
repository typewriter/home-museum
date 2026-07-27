#!/usr/bin/env ruby

require_relative "kv_store"
require "json"
require "sqlite3"
require "rexml/document"

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
SQL
db.execute(sql)
db.execute("CREATE INDEX IF NOT EXISTS images_style ON images (style);")
db.execute("CREATE UNIQUE INDEX IF NOT EXISTS images_source ON images (source_url);")



def aic_loader(path)
  records = []
  db = KVStore.new path
  db.each { |id, v|
    json = JSON.parse(v)
    next if !json["is_public_domain"]
    next if !json["image_id"]

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
      image_url: "https://www.artic.edu/iiif/2/#{json["image_id"]}/full/full/0/default.jpg",
    }
  }
  records
end

def met_loader(path)
  records = []
  db = KVStore.new path
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

def smithsonian_loader(path)
  records = []
  db = KVStore.new path
  db.each { |id, v|
    json = JSON.parse(v)
    dnr = json.dig("content", "descriptiveNonRepeating") || {}
    freetext = json.dig("content", "freetext") || {}

    next if dnr.dig("metadata_usage", "access") != "CC0"
    next if !dnr["record_link"]

    media = (dnr.dig("online_media", "media") || []).find { |m| m["type"] == "Images" && m.dig("usage", "access") == "CC0" }
    next if !media

    image_url = media["resources"]&.find { |r| r["label"] == "High-resolution JPEG" }&.dig("url") || media["content"]
    next if !image_url

    names = freetext["name"] || []
    department = (freetext["setName"] || []).find { |e| e["label"] == "Department" }
    physical = freetext["physicalDescription"] || []

    records << {
      source: 'Smithsonian',
      category: freetext.dig("objectType", 0, "content"),
      style: department&.dig("content"),
      title: dnr.dig("title", "content"),
      artist: names.map { |n| n["content"] }.compact.join(', '),
      date: freetext.dig("date", 0, "content"),
      medium: physical.find { |e| e["label"] == "Medium" }&.dig("content"),
      origin: nil,
      dimensions: physical.find { |e| e["label"] == "Dimensions" }&.dig("content"),
      credit: freetext.dig("creditLine", 0, "content"),
      description: nil,
      source_url: dnr["record_link"],
      image_url: image_url,
    }
  }
  records
end

def paris_loader(path)
  records = []
  db = KVStore.new path
  db.each { |id, v|
    json = JSON.parse(v)

    visuals = (json["fieldVisuels"] || []).select { |e| e.dig("entity","publicUrl") != nil && e.dig("entity","fieldImageLibre") == true }
    next if visuals.empty?

    visuals.each_with_index { |principal, i|
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

def rijksmuseum_loader(path)
  records = []
  db = KVStore.new path
  db.each { |id, v|
    json = JSON.parse(v)
    doc = REXML::Document.new(json["rdf_xml"])

    # rdf:RDF 直下の各リソース(edm:Agent/edm:Place/skos:Concept 等)を
    # rdf:about で引けるようにしておく。dc:creator 等はこれらへの URI 参照のため
    resources = {}
    doc.root.elements.each { |el|
      about = el.attributes["rdf:about"]
      resources[about] = el if about
    }

    label_for = lambda { |uri|
      next nil if !uri
      el = resources[uri]
      next nil if !el
      prefs = el.get_elements("skos:prefLabel")
      (prefs.find { |e| e.attributes["xml:lang"] == "en" } || prefs.first)&.text
    }

    text_for = lambda { |elements|
      (elements.find { |e| e.attributes["xml:lang"] == "en" } || elements.first)&.text
    }

    aggregation = doc.get_elements("//ore:Aggregation").first
    next if !aggregation

    rights = aggregation.get_elements("edm:rights").first&.attributes&.[]("rdf:resource")
    next if !rights&.include?("publicdomain")

    image_url = aggregation.get_elements("edm:isShownBy").first&.attributes&.[]("rdf:resource")
    next if !image_url

    cho = doc.get_elements("//edm:ProvidedCHO").first
    next if !cho

    records << {
      source: 'Rijksmuseum',
      category: label_for.call(cho.get_elements("dc:type").first&.attributes&.[]("rdf:resource")),
      style: nil,
      title: text_for.call(cho.get_elements("dc:title")),
      artist: cho.get_elements("dc:creator").map { |e| label_for.call(e.attributes["rdf:resource"]) }.compact.join(", "),
      date: text_for.call(cho.get_elements("dcterms:created")),
      medium: label_for.call(cho.get_elements("dcterms:medium").first&.attributes&.[]("rdf:resource")),
      origin: label_for.call(cho.get_elements("dcterms:spatial").first&.attributes&.[]("rdf:resource")),
      dimensions: text_for.call(cho.get_elements("dcterms:extent")),
      credit: nil,
      description: text_for.call(cho.get_elements("dc:description")),
      source_url: aggregation.get_elements("edm:isShownAt").first&.attributes&.[]("rdf:resource"),
      image_url: image_url,
    }
  }
  records
end

def cleveland_loader(path)
  records = []
  db = KVStore.new path
  db.each { |id, v|
    json = JSON.parse(v)
    next if json["share_license_status"] != "CC0"

    image_url = json.dig("images", "print", "url") || json.dig("images", "web", "url")
    next if !image_url

    records << {
      source: 'Cleveland Museum of Art',
      category: json["type"],
      style: json["department"],
      title: json["title"],
      artist: (json["creators"] || []).map { |c| c["description"] }.compact.join(", "),
      date: json["creation_date"],
      medium: json["technique"],
      origin: (json["culture"] || []).compact.join(", "),
      dimensions: json["measurements"],
      credit: json["creditline"],
      description: json["description"],
      source_url: json["url"],
      image_url: image_url,
    }
  }
  records
end

# insert or update
records = aic_loader(ARGV[0])
records += met_loader(ARGV[1])
records += paris_loader(ARGV[2])
records += rijksmuseum_loader(ARGV[3])
records += smithsonian_loader(ARGV[4])
records += cleveland_loader(ARGV[5])

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
      db.execute(sql, [
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
                 ])
    }
  }
}

db.execute("reindex")
db.execute("vacuum")
db.execute("PRAGMA journal_mode = delete")
