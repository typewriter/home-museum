#!/usr/bin/env ruby

# image_artists.role_bucket を埋める。設計は spec_normalization_author.md。
#
#   ruby normalize_artists.rb            全件
#   ruby normalize_artists.rb cleveland  ソース指定
#
# Smithsonian の freetext.name[] には Sitter (肖像画のモデル) や Patron が
# 混ざっており、そのまま作者として扱うと「制作者ではない人物が artist として
# 表示される」。role_raw / qualifier_raw をバケットに分類して切り分ける。
#
# バケット:
#   creator            制作者 (確度高)
#   creator_uncertain  帰属が推定 (attributed to / circle of / workshop of …)
#   after              原作者・引用元。本人の制作ではない (after / copy after)
#   non_creator        制作に関与していない (sitter / patron / owner …)
#   unknown            役割はあるが、どのバケットにも判別できなかった
#   NULL               そもそも役割情報が無いソース (Rijksmuseum, AIC)
#
# NULL と unknown を分けているのは、「判定材料が無い」と「材料はあるが語彙に
# 無い」を区別するため。以前はどちらも creator を既定値にしていたため、
# 判定結果としての creator と混ざって区別できなくなっていた。
#
# 名寄せ (同一人物の統合) はここではやらない。日本語名は image_artist_names が
# エントリ単位で持つので、名寄せ無しでも表示は成立する。

require_relative "db"
require_relative "loaders"

RULE_VERSION = "2026-08-01"

NON_CREATOR = /\b(sitter|patron|dedicat\w*|collector|client|commission\w*|owner|subject|addressee|associated)\b|person in photograph|restorer|conservator/
UNCERTAIN   = /\b(attribut\w*|possibl\w*|probabl\w*|circle of|follower of|school of|workshop of|style of|manner of|ascribed)\b/
AFTER       = /\b(after|copy after|cast after|derived from|d'après|d'apres)\b/

# creator は積極判定にする。ここに無い役割は creator ではなく unknown に落ちる。
# 語彙が館ごとに違う (Cleveland は "printed by" のような句、Paris Musées は仏語)
# ため、単語境界は使わず部分一致で拾う。NON_CREATOR/AFTER/UNCERTAIN を先に
# 見るので、"Person in Photograph" が photograph で creator になることはない。
CREATOR = Regexp.union(
  # 英語 (Met / Smithsonian / Cleveland)
  "artist", "design", "publish", "print", "maker", "made", "make", "manufact",
  "factory", "engrav", "lithograph", "etch", "aquatint", "decorat", "model",
  "draftsman", "found", "illustrat", "calligraph", "paint", "sculpt",
  "architect", "photograph", "embroider", "weav", "woven", "potter", "ceramist",
  "goldsmith", "silversmith", "gild", "binder", "carv", "enamel", "armorer",
  "gunsmith", "gunmaker", "bladesmith", "swordsmith", "barrelsmith", "sword",
  "hilt", "chisel", "woodcutter", "block cut", "dyer", "jewel", "cartograph",
  "cartoonist", "fabricat", "creator", "execut", "cast", "thrower", "workmaster",
  "cabinetmaker", "clockmaker", "movement", "mount", "stock", "damascener",
  "lacquer", "glaze", "colorist", "colored", "miniatur", "caster", "delineator",
  # フランス語 (Paris Musées)
  "dessinateur", "graveur", "imprimeur", "editeur", "éditeur", "sculpteur",
  "peintre", "aquafortiste", "aquarelliste", "pastelliste", "buriniste",
  "céramiste", "faïencier", "illustrateur", "enlumineur", "relieur", "fabricant",
  "orfèvre", "potier", "fondeur", "bronzier", "ébéniste", "ebéniste", "émailleur",
  "emailleur", "verrier", "vitrailliste", "architecte", "couturier", "couturière",
  "modeleur", "maquettiste", "modéliste", "menuisier", "typographe",
  "miniaturiste", "ferronnier", "cordonnier", "bottier", "fourreur", "pelletier",
  "brodeur", "costumier", "ivoirier", "ciseleur", "doreur", "tailleur",
  "chausseur", "tapissier", "mosaïste", "encadreur", "tisseur", "lissier",
  "dentellière", "luthier", "parurier", "graphiste", "affichiste", "styliste",
  "calligraphe", "mouleur", "parfumeur", "maroquinier", "passementier",
  "dinandier", "bonnetier", "gantier", "corsetier", "boutonnier", "eventailliste",
  "bijoutier", "joaillier", "modiste", "chapelier",
  "photographe", "horloger", "tabletier", "caricaturiste", "coloriste",
  "décorateur", "glyptographe", "cartographe", "papetier", "soyeux",
  "atelier de production", "maison de couture", "maison de confection",
  "auteur du modèle", "créateur", "facteur d'instrument", "tireur de photographies"
)

def bucket(role_raw, qualifier_raw)
  role = role_raw.to_s.downcase
  qualifier = qualifier_raw.to_s.downcase

  # 役割情報を持たないソース。creator を既定値にすると「判定した creator」と
  # 区別が付かなくなるので、判定しなかったことを NULL で残す
  return nil if role.empty? && qualifier.empty?

  # Cleveland は確度を role ではなく qualifier に持つので先に見る
  return "after" if qualifier.match?(AFTER)
  return "creator_uncertain" if qualifier.match?(UNCERTAIN)

  return "non_creator" if role.match?(NON_CREATOR)
  return "after" if role.match?(AFTER)
  return "creator_uncertain" if role.match?(UNCERTAIN)
  return "creator" if role.match?(CREATOR)
  "unknown"
end

sources = ARGV
sources.each { |s| abort "未知のソース: #{s} (#{Loaders::SOURCES.keys.join(', ')})" if !Loaders::SOURCES.key?(s) }

db = DB.open
where = sources.empty? ? "" : "and i.source in (#{sources.map { '?' }.join(', ')})"

stats = Hash.new(0)
last_id = 0

loop do
  rows = db.execute(
    "select a.id, a.role_raw, a.qualifier_raw from image_artists a " \
    "join images i on i.id = a.image_id " \
    "where a.id > ? #{where} order by a.id limit 5000",
    [last_id] + sources
  )
  break if rows.empty?

  db.transaction {
    rows.each { |id, role_raw, qualifier_raw|
      value = bucket(role_raw, qualifier_raw)
      stats[value] += 1
      db.execute("update image_artists set role_bucket = ? where id = ? and role_bucket IS NOT ?",
                 [value, id, value])
      last_id = id
    }
  }
  STDERR.print "\r#{stats.values.sum}件"
end

STDERR.puts "\r#{stats.values.sum}件"
stats.sort_by { |_, v| -v }.each { |value, count| puts "  #{value || '(NULL: 役割情報なし)'}: #{count}" }

DB.finalize(db)
db.close
