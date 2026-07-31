require_relative "kv_store"
require "json"
require "cgi"
require "rexml/document"

# 各美術館の LMDB を images / image_artists と同じ共通スキーマのレコードへ変換する。
#
# LMDB を走査するのはここだけ (loader.rb と title_translation_batch.rb が使う)。
# Rijksmuseum の RDF パースは全走査に数十分かかるため、正規化ルールの適用は
# LMDB ではなく DB を読む別スクリプト (normalize_*.rb) 側に置いてある。
#
# hm.db への投入 (loader.rb) とタイトル日本語訳 (title_translation_batch.rb) は
# 「どのレコードを対象とするか」の条件と source_url の作り方が一致していないと
# CSV が images に JOIN できなくなるため、抽出はここ1箇所だけに実装する。
#
# レコードの形:
#   source, source_id, source_url, image_url, title, artist, date,
#   date_raw_start, date_raw_end, date_raw_precision,
#   category, style, medium, origin, dimensions, credit, description,
#   artists: [{ name_raw, role_raw, qualifier_raw, birth_year, death_year,
#               source_artist_id, authority_urls, name_original_language }, ...]
#
# ここに入れてよいのは「館のデータをそのまま写した値」だけ。解釈が要るもの
# (date_precision の分類、役割のバケット分け、名寄せ) は派生層の仕事。
module Loaders
  BASE_DIR = File.dirname(File.expand_path(__FILE__))

  # ソース名 → images.source に入るキーと、既定の LMDB ファイル名。
  # label は人間向けの表示名で、DB には入らない (CSV の可読性のためだけに使う)。
  SOURCES = {
    "aic"         => { label: "AIC",                     lmdb: "aic.lmdb" },
    "met"         => { label: "MET",                     lmdb: "met.lmdb" },
    "parismusees" => { label: "Paris Musées",            lmdb: "parismusees.lmdb" },
    "rijksmuseum" => { label: "Rijksmuseum",             lmdb: "rijksmuseum.lmdb" },
    "smithsonian" => { label: "Smithsonian",             lmdb: "smithsonian.lmdb" },
    "cleveland"   => { label: "Cleveland Museum of Art", lmdb: "cleveland.lmdb" },
  }.freeze

  module_function

  def label(name)
    SOURCES.fetch(name)[:label]
  end

  def lmdb_path(name)
    "#{BASE_DIR}/#{SOURCES.fetch(name)[:lmdb]}"
  end

  # name のソースを走査してレコードを1件ずつ yield する。
  # path 省略時は BASE_DIR 直下の既定 LMDB を見る。
  def each_record(name, path = nil, &block)
    SOURCES.fetch(name) # 未知の名前はここで弾く
    send(name, path || lmdb_path(name), &block)
  end

  def records(name, path = nil)
    out = []
    each_record(name, path) { |record| out << record }
    out
  end

  # LMDB を開いてパース済みの JSON を1件ずつ yield する。
  # 呼び出し側が途中で走査を打ち切っても env を閉じる
  # (閉じ忘れると "closing environment with open transactions" が出る)。
  def each_entry(path)
    store = KVStore.new(path)
    store.each { |_id, v| yield JSON.parse(v) }
  ensure
    store&.close
  end

  # "1863" / 1863 / "1863-05-01" / nil → Integer | nil
  def year(value)
    return nil if value.nil?
    return value if value.is_a?(Integer)
    m = value.to_s.strip.match(/\A(-?\d{1,6})/)
    m && m[1].to_i
  end

  def present(value)
    s = value.to_s
    s.empty? ? nil : s
  end

  # 空文字を除いた典拠URLの配列を JSON 文字列にする (無ければ nil)。
  def authority_urls(*urls)
    list = urls.flatten.map { |u| present(u) }.compact.uniq
    list.empty? ? nil : JSON.generate(list)
  end

  def aic(path)
    each_entry(path) { |json|
      next if !json["is_public_domain"]
      next if !json["image_id"]

      # artist_ids / artist_titles は同じ並びの構造化フィールド。
      # artist_display は複数行に "after ○○" 等を含むが構造化されていないので
      # images.artist に生のまま残すだけにする。
      ids = json["artist_ids"] || []
      artists = (json["artist_titles"] || []).each_with_index.filter_map { |name, i|
        next if !present(name)
        { name_raw: name, source_artist_id: present(ids[i]) }
      }

      yield({
        source: "aic",
        source_id: present(json["id"]),
        category: json["classification_title"],
        style: json["style_title"],
        title: json["title"],
        artist: json["artist_display"],
        date: json["date_display"],
        date_raw_start: year(json["date_start"]),
        date_raw_end: year(json["date_end"]),
        date_raw_precision: present(json["date_qualifier_title"]),
        medium: json["medium_display"],
        origin: json["place_of_origin"],
        dimensions: json["dimensions"],
        credit: json["credit_line"],
        description: json["description"],
        source_url: "https://www.artic.edu/artworks/#{json["id"]}",
        image_url: "https://www.artic.edu/iiif/2/#{json["image_id"]}/full/full/0/default.jpg",
        artists: artists,
      })
    }
  end

  def met(path)
    each_entry(path) { |json|

      next if !json["isPublicDomain"]
      next if !json["primaryImage"]

      constituents = json["constituents"] || []
      # artistBeginDate/EndDate は主たる作者1人分しか無いので、
      # 作者が1人に確定しているときだけ紐づける。
      solo = constituents.size <= 1
      artists = constituents.filter_map { |c|
        name = present(c["name"])
        next if !name
        {
          # Met の名前は HTML エスケープされている ("B. Altman &amp; Co.")
          name_raw: CGI.unescapeHTML(name),
          role_raw: present(c["role"]),
          birth_year: solo ? year(json["artistBeginDate"]) : nil,
          death_year: solo ? year(json["artistEndDate"]) : nil,
          source_artist_id: present(c["constituentID"]),
          authority_urls: authority_urls(c["constituentULAN_URL"], c["constituentWikidata_URL"]),
        }
      }
      # constituents が無く artistDisplayName だけあるレコードが少数ある
      if artists.empty? && present(json["artistDisplayName"])
        artists = [{
          name_raw: CGI.unescapeHTML(json["artistDisplayName"]),
          role_raw: present(json["artistRole"]),
          birth_year: year(json["artistBeginDate"]),
          death_year: year(json["artistEndDate"]),
          authority_urls: authority_urls(json["artistULAN_URL"], json["artistWikidata_URL"]),
        }]
      end

      yield({
        source: "met",
        source_id: present(json["objectID"]),
        category: json["classification"],
        style: json["department"],
        title: json["title"],
        artist: present(json["artistDisplayName"]) && CGI.unescapeHTML(json["artistDisplayName"]),
        date: json["objectDate"],
        date_raw_start: year(json["objectBeginDate"]),
        date_raw_end: year(json["objectEndDate"]),
        date_raw_precision: nil,
        medium: json["medium"],
        origin: nil,
        dimensions: json["dimensions"],
        credit: json["creditLine"],
        description: nil,
        source_url: json["objectURL"],
        image_url: json["primaryImage"],
        artists: artists,
      })
    }
  end

  def smithsonian(path)
    each_entry(path) { |json|
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

      # content に "Titian, Italian, ca. 1488 - 1576" のように国籍・生没年が
      # 混ざっている。生没年の抽出は正規表現が要る = 解釈なのでここではやらない。
      artists = names.filter_map { |n|
        name = present(n["content"])
        next if !name
        { name_raw: name, role_raw: present(n["label"]) }
      }

      yield({
        source: "smithsonian",
        source_id: present(dnr["record_ID"]),
        category: freetext.dig("objectType", 0, "content"),
        style: department&.dig("content"),
        title: dnr.dig("title", "content"),
        artist: names.map { |n| n["content"] }.compact.join(', '),
        date: freetext.dig("date", 0, "content"),
        date_raw_start: nil,
        date_raw_end: nil,
        date_raw_precision: nil,
        medium: physical.find { |e| e["label"] == "Medium" }&.dig("content"),
        origin: nil,
        dimensions: physical.find { |e| e["label"] == "Dimensions" }&.dig("content"),
        credit: freetext.dig("creditLine", 0, "content"),
        description: nil,
        source_url: dnr["record_link"],
        image_url: image_url,
        artists: artists,
      })
    }
  end

  def parismusees(path)
    each_entry(path) { |json|

      visuals = (json["fieldVisuels"] || []).select { |e| e.dig("entity","publicUrl") != nil && e.dig("entity","fieldImageLibre") == true }
      next if visuals.empty?

      production = json["fieldDateProduction"] || {}
      artists = (json["fieldOeuvreAuteurs"] || []).filter_map { |e|
        author = e.dig("entity", "fieldAuteurAuteur", "entity")
        name = author && present(author["name"])
        next if !name
        {
          name_raw: name,
          role_raw: e.dig("entity", "fieldAuteurFonction", "entity", "name"),
          birth_year: year(author.dig("fieldPipDateNaissance", "startYear")),
          death_year: year(author.dig("fieldPipDateDeces", "startYear")),
        }
      }

      # startYear が無いレコードが約半数ある。世紀の自由記述しか無い場合は
      # それを表示用テキストに回す (数値化は normalize_dates.rb の仕事)。
      date = ((production["startYear"] || "").to_s + "-" + (production["endYear"] || "").to_s).gsub(/-$/, "")
      date = present(production["century"]) || present(json.dig("fieldOeuvreSiecle", 0, "entity", "name")) if date.empty?

      visuals.each_with_index { |principal, i|
        yield({
          source: "parismusees",
          source_id: present(json["entityId"]),
          category: (json["fieldOeuvreTypesObjet"] || []).map{|e|e.dig("entity","name")}.compact.join(','),
          style: (json["fieldOeuvreThemeRepresente"] || []).map{|e|e.dig("entity","name")}.compact.join(','),
          title: json["title"],
          artist: artists.map { |a| a[:name_raw] }.join(','),
          date: date,
          date_raw_start: year(production["startYear"]),
          date_raw_end: year(production["endYear"]),
          # "En"/"Vers"/"Entre"/"Après"/"Avant" 等の仏語。endPrecision は
          # "Entre ... et" のペアの片割れなので start 側だけで判別できる。
          date_raw_precision: present(production["startPrecision"]),
          medium: (json["fieldMateriauxTechnique"] || []).map{|e|e.dig("entity","name")}.compact.join(','),
          origin: nil,
          dimensions: nil,
          credit: nil,
          description: nil,
          source_url: json["absolutePath"] + "?#{i}",
          image_url: principal.dig("entity","publicUrl"),
          artists: artists,
        })
      }
    }
  end

  def rijksmuseum(path)
    each_entry(path) { |json|
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

      # dc:creator は edm:Agent への URI 参照。Agent 側に生没年と
      # owl:sameAs (VIAF/Wikidata/ULAN/RKD) が揃っている。
      artists = cho.get_elements("dc:creator").filter_map { |ref|
        uri = ref.attributes["rdf:resource"]
        name = label_for.call(uri)
        next if !present(name)
        agent = resources[uri]
        {
          name_raw: name,
          birth_year: year(agent&.get_elements("rdaGr2:dateOfBirth")&.first&.text),
          death_year: year(agent&.get_elements("rdaGr2:dateOfDeath")&.first&.text),
          source_artist_id: uri,
          authority_urls: authority_urls(
            (agent&.get_elements("owl:sameAs") || []).map { |e| e.attributes["rdf:resource"] }
          ),
        }
      }

      yield({
        source: "rijksmuseum",
        source_id: present(cho.attributes["rdf:about"]),
        category: label_for.call(cho.get_elements("dc:type").first&.attributes&.[]("rdf:resource")),
        style: nil,
        title: text_for.call(cho.get_elements("dc:title")),
        artist: artists.map { |a| a[:name_raw] }.join(", "),
        date: text_for.call(cho.get_elements("dcterms:created")),
        date_raw_start: nil,
        date_raw_end: nil,
        date_raw_precision: nil,
        medium: label_for.call(cho.get_elements("dcterms:medium").first&.attributes&.[]("rdf:resource")),
        origin: label_for.call(cho.get_elements("dcterms:spatial").first&.attributes&.[]("rdf:resource")),
        dimensions: text_for.call(cho.get_elements("dcterms:extent")),
        credit: nil,
        description: text_for.call(cho.get_elements("dc:description")),
        source_url: aggregation.get_elements("edm:isShownAt").first&.attributes&.[]("rdf:resource"),
        image_url: image_url,
        artists: artists,
      })
    }
  end

  def cleveland(path)
    each_entry(path) { |json|
      next if json["share_license_status"] != "CC0"

      image_url = json.dig("images", "print", "url") || json.dig("images", "web", "url")
      next if !image_url

      # creators[].id は館内部の作者IDで、生没年も構造化済み。
      # name_in_original_language には漢字名が入ることがある (羅稚川 / 勝川 春章)。
      artists = (json["creators"] || []).filter_map { |c|
        name = present(c["description"])
        next if !name
        {
          name_raw: name,
          role_raw: present(c["role"]),
          qualifier_raw: present(c["qualifier"]),
          birth_year: year(c["birth_year"]),
          death_year: year(c["death_year"]),
          source_artist_id: present(c["id"]),
          name_original_language: present(c["name_in_original_language"]),
        }
      }

      yield({
        source: "cleveland",
        source_id: present(json["id"]),
        category: json["type"],
        style: json["department"],
        title: json["title"],
        artist: (json["creators"] || []).map { |c| c["description"] }.compact.join(", "),
        date: json["creation_date"],
        date_raw_start: year(json["creation_date_earliest"]),
        date_raw_end: year(json["creation_date_latest"]),
        date_raw_precision: nil,
        medium: json["technique"],
        origin: (json["culture"] || []).compact.join(", "),
        dimensions: json["measurements"],
        credit: json["creditline"],
        description: json["description"],
        source_url: json["url"],
        image_url: image_url,
        artists: artists,
      })
    }
  end
end
