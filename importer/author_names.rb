# 作者名の正規化。normalize_person.rb が使う。
# 判断の根拠は docs/spec_normalization.md。

module AuthorNames
  # 典拠の代表を選ぶ優先順位。ULAN を先頭にしているのは美術分野で最も網羅的なため。
  AUTHORITY_RANK = %w[ulan wd viaf rkd].freeze

  module_function

  # 名寄せ対象から外す名前。同じ「不明」同士を1人に融合させないため。
  # 作者エントリの約2割 (28万件) がここに該当する。
  PLACEHOLDER_NAME = /\A(unknown|unidentified|anonym\w*|anoniem|onbekend|inconnu\w*|
                         non identifi\w*|n ?d|none|various|maker unknown|no artist|
                         not known)\b/x

  # 典拠ID側のプレースホルダー。ULAN 500397994 と Wikidata Q4233718 はどちらも
  # 「作者不明」を指すので、これで束ねると 19万件超が1クラスタになる。
  PLACEHOLDER_AUTHORITY = %w[ulan:500397994 wd:Q4233718].freeze

  # 役割の接頭辞。Met は "After ○○" のように役割を名前文字列に埋め込んでおり
  # role_bucket では捕まらない (Met の after バケットは0件)。名寄せキーの算出でも
  # display_name の算出でも落とす必要がある。
  ROLE_PREFIX = /\A(
    # "Attributed here to" "Attributed possibly to" "Traditionally attributed to"
    # "Attributed by accompanying document to" のような回りくどい言い回しがある。
    # 末尾の to を必須にして、名前そのものを食べないようにする
    (formerly\s+|traditionally\s+)?attributed(\s+\w+){0,4}?\s+to |
    # 単独の語は後ろに空白を要求する。そうしないと "and" が "Andrea del Sarto"、
    # "after" が "Afterman" の先頭を食べる
    possibly(\s+by)?(?=\s) | probably(?=\s) |
    # "After designs by" "After a painting by" 等。同じく末尾の by を必須にする
    after(\s+\w+){1,3}?\s+by | after(?=\s) | and(?=\s) |
    copy\s+(after|of) | cast\s+after | derived\s+from |
    workshop\s+of | circle\s+of | follower\s+of | style\s+of | school\s+of |
    manner\s+of | made\s+by | designed\s+by | design\s+attributed\s+to |
    engraved\s+by | published\s+by | printed\s+by | related\s+to | based\s+on |
    adapted\s+from | \(\?\) | \?
  )\s*/xi

  # Met の "Watchmaker: Vaucher Fréres"。コロンの前を無条件に剥がすと
  # "Le Caricaturiste :" (新聞名) のように名前そのものが消えるので、役割語を列挙する。
  COLON_ROLE = /\A(
    medalist | watchmaker | clockmaker | case\s+maker | gilder | designer |
    printer | silversmith | goldsmith | engraver | jeweler | enameler |
    cabinetmaker | founder | sculptor | painter | draftsman
  )(\s*\([^)]*\))?\s*:\s*/xi

  # 同一の (source, birth_year, death_year) をこれ以上の人数が共有していたら、
  # 実在の伝記情報ではなく館が埋めた推定値・番兵とみなして検証に使わない。
  #   Paris Musées  1770/nil          520人 ("(signataire)" 群)
  #   Met           nil/9999          120人 (番兵)
  #   Met           1800/1900 など    106人 (活動期の100年幅を生没年欄に入れている)
  YEAR_COHORT_MIN = 20
  SENTINEL_YEARS = [0, 9999].freeze

  # 名前だけを根拠にする段階で、同名グループがこれを超えたら諦める。
  MAX_GROUP = 200

  def strip_role_prefix(name)
    previous = nil
    current = name.to_s.strip
    # "? After a painting by ○○" のように重なることがあるので落ちなくなるまで繰り返す
    while current != previous
      previous = current
      current = current.sub(COLON_ROLE, "").sub(ROLE_PREFIX, "").strip
    end
    current
  end

  # 表示・照合に使う中核部分を取り出す。括弧を落とすかどうかはソースごとに違う
  # (docs/spec_normalization.md「括弧の扱いをソースごとに変えた」)。
  def name_core(raw, source)
    name = strip_role_prefix(raw)

    case source
    when "smithsonian"
      # "Johann Esaias Nilson, German, 1721 - 1788" → 最初のカンマまでが名前。
      # 先に括弧を落とすのは "Katsukawa Shunkō II (Shunsen) (Japanese, c. 1762-…)" の
      # ように括弧の中にカンマが来る形があるため
      name = name.sub(/\s*\(.*\z/m, "")
      name = split_at_comma(name)&.first || name
    when "parismusees"
      # "Morisseau, Eugène" → "Eugène Morisseau"。括弧は落とさず末尾に残す。
      # そうしないと "Atget, Eugène (…, dit)" が "Eugène (…, dit) Atget" になる
      suffix = ""
      if (open = trailing_paren_at(name))
        suffix = " #{name[open..]}"
        name = name[0, open].strip
      end
      parts = split_at_comma(name)
      name = "#{parts[1]} #{parts[0]}" if parts
      name += suffix
    when "rijksmuseum"
      # 括弧は識別子。落とさない
    else
      name = name.sub(/\s*\(.*\z/m, "")
    end

    name.strip
  end

  def trailing_paren_at(name)
    return nil if !name.end_with?(")")

    depth = 0
    (name.length - 1).downto(0) { |i|
      case name[i]
      when ")" then depth += 1
      when "("
        depth -= 1
        return i if depth.zero?
      end
    }
    nil
  end

  # 括弧の外にある最初のカンマで2つに割る。無ければ nil。括弧内にカンマを持つ名前
  # ("Aubert (Imprimeur, lithographe, éditeur)") があるので split(",") にしない。
  def split_at_comma(name)
    depth = 0
    name.each_char.with_index { |char, i|
      case char
      when "(" then depth += 1
      when ")" then depth -= 1 if depth > 0
      when ","
        return [name[0, i].strip, name[(i + 1)..].strip] if depth.zero?
      end
    }
    nil
  end

  def name_key(core)
    core.unicode_normalize(:nfkd).gsub(/\p{Mn}/, "").downcase.gsub(/[^a-z0-9]+/, " ").strip
  end

  def placeholder_key?(key)
    key.empty? || key.length < 3 || key.match?(PLACEHOLDER_NAME)
  end
end
