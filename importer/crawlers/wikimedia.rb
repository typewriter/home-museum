#!/usr/bin/env ruby
# frozen_string_literal: true

# Wikidata の truthy ダンプ (N-Triples, 数十GB) を丸ごとストリーム処理し、
# 対象の作品種別 (painting/drawing/print/mosaic/fresco) のうち
# 著作権状態がパブリックドメインで画像を持つエンティティだけを LMDB に落とす。
#
# 他のクローラーと違い、単一の館ダンプAPIをページングするのではなく
# dumps.wikimedia.org が配布する全世界共通の1ファイルをフィルタする形になる
# (AIC/METのようなREST APIも、カテゴリ巡回もしない。理由は README 参照)。
#
#   ruby wikimedia.rb          対象作品を crawlers/wikimedia.lmdb へ抽出
#   ruby wikimedia.rb enrich   抽出済みレコードが参照する作者(creator)の
#                              名前・生没年を Wikidata API から補う (唯一のライブHTTP)
#
# ダンプは curl | bzcat のパイプでストリームするだけで、ディスクには保存しない
# (40GB超の圧縮ファイルを保存する余裕が無い環境でも動く)。
# 中断からの再開は無い (AIC と同じ割り切り。再実行すると最初からになるが、
# 既に書いた QID は KVStore#has? で検出してスキップするため書き込みの重複は起きない)。

require 'json'
require 'time'
require 'set'
require 'net/http'
require 'uri'
require_relative '../kv_store'

module Wikimedia
  DUMP_URL = 'https://dumps.wikimedia.org/wikidatawiki/entities/latest-truthy.nt.bz2'

  ENTITY_PREFIX = 'http://www.wikidata.org/entity/'
  PROP_PREFIX = 'http://www.wikidata.org/prop/direct/'
  LABEL_PRED = 'http://www.w3.org/2000/01/rdf-schema#label'

  # instance of (P31) がこのいずれかであれば取り込む。QID → 英語ラベルの
  # 対応表を兼ねており、loaders.rb はカテゴリ表示にこの定数をそのまま再利用する
  # (QIDリストの二重管理を避けるため、対象クラスの定義はここ1箇所だけに置く)。
  # 拡張するときはここに追記するだけでよい (spec は importer/README.md)。
  TARGET_CLASSES = {
    'Q3305213'  => 'painting',
    'Q93184'    => 'drawing',
    'Q11060274' => 'print',
    'Q133067'   => 'mosaic',
    'Q22669139' => 'fresco',
  }.freeze
  TARGET_CLASS_QIDS = TARGET_CLASSES.keys.to_set.freeze

  PUBLIC_DOMAIN = 'Q19652' # copyright status (P6216) の値

  # このプロパティだけ値を溜める。他は読み飛ばして解析コストを削る。
  WANTED_PROPS = %w[P31 P6216 P18 P170 P571 P572 P1476].to_set.freeze

  USER_AGENT = 'HomeMuseumImporter/1.0 (https://github.com/typewriter/home-museum; ' \
               'importer crawler for a public-domain art slideshow) Ruby'

  LMDB_PATH = "#{File.dirname(__FILE__)}/wikimedia.lmdb"

  module_function

  # ===================== NTriples の最小パーサ =====================
  #
  # truthy ダンプは1行1トリプル (<s> <p> o .) で、値は必ず \uXXXX / \UXXXXXXXX
  # でエスケープされている (実測: 生UTF-8ではない)。フルのNTriples文法は
  # 実装せず、このダンプで実際に出てくる形だけを扱う。

  # "..."(@lang | ^^<...>)? → [値, 言語 or nil]。リテラルでなければ nil。
  def parse_literal(object_str)
    return nil unless object_str.start_with?('"')

    len = object_str.length
    i = 1
    buf = String.new(encoding: Encoding::UTF_8)
    while i < len
      c = object_str[i]
      if c == '\\'
        nc = object_str[i + 1]
        case nc
        when '"' then buf << '"'; i += 2
        when '\\' then buf << '\\'; i += 2
        when 'n' then buf << "\n"; i += 2
        when 't' then buf << "\t"; i += 2
        when 'r' then buf << "\r"; i += 2
        when 'u'
          code = object_str[i + 2, 4].to_i(16)
          if (0xD800..0xDBFF).cover?(code) && object_str[i + 6, 2] == '\\u'
            low = object_str[i + 8, 4].to_i(16)
            if (0xDC00..0xDFFF).cover?(low)
              buf << [0x10000 + ((code - 0xD800) * 0x400) + (low - 0xDC00)].pack('U')
              i += 12
              next
            end
          end
          buf << [code].pack('U')
          i += 6
        when 'U'
          buf << [object_str[i + 2, 8].to_i(16)].pack('U')
          i += 10
        else
          buf << nc.to_s
          i += 2
        end
      elsif c == '"'
        i += 1
        break
      else
        buf << c
        i += 1
      end
    end
    suffix = object_str[i..] || ''
    [buf, suffix.start_with?('@') ? suffix[1..] : nil]
  end

  # <http://www.wikidata.org/entity/Q123> → "Q123"。エンティティURIでなければ nil。
  def qid_of(object_str)
    return nil unless object_str.start_with?('<') && object_str.end_with?('>')
    uri = object_str[1..-2]
    return nil unless uri.start_with?(ENTITY_PREFIX)
    uri[ENTITY_PREFIX.length..]
  end

  # <...> → 中身のURI文字列。エンティティ参照とは限らない (P18 は Commons URL)。
  def uri_of(object_str)
    return nil unless object_str.start_with?('<') && object_str.end_with?('>')
    object_str[1..-2]
  end

  # 1行を [subject, predicate, object_str] に分解。object_str は "<...>" か
  # "\"...\"" のまま (末尾の " ." は除去済み)。パースできなければ nil。
  def split_triple(line)
    return nil unless line.start_with?('<') && line.end_with?(' .')
    line = line[0..-3]
    s_end = line.index('>')
    return nil unless s_end
    subject = line[1...s_end]
    rest = line[(s_end + 2)..]
    return nil unless rest&.start_with?('<')
    p_end = rest.index('>')
    return nil unless p_end
    predicate = rest[1...p_end]
    object_str = rest[(p_end + 2)..]
    return nil unless object_str
    [subject, predicate, object_str]
  end

  # ===================== エンティティ組み立て =====================

  # subject は生URI (例: "http://www.wikidata.org/entity/Q42")、qid はその短縮形。
  # 行ごとの主語判定は subject 同士 (どちらも生URI) で比較しないと常に不一致になる。
  Entity = Struct.new(:subject, :qid, :props, :label_en, :label_ja) do
    def initialize
      super(nil, nil, Hash.new { |h, k| h[k] = [] }, nil, nil)
    end
  end

  # 溜めた1エンティティぶんの props から、対象条件を満たせばレコードを返す。
  # 満たさなければ nil (呼び出し側は無視するだけでよい)。
  def build_record(qid, props, label_en, label_ja)
    instance_of = props['P31'].filter_map { |o| qid_of(o) }
    return nil if (instance_of.to_set & TARGET_CLASS_QIDS).empty?

    copyright = props['P6216'].filter_map { |o| qid_of(o) }
    return nil unless copyright.include?(PUBLIC_DOMAIN)

    image_url = props['P18'].filter_map { |o| uri_of(o) }.first
    return nil unless image_url

    titles = {}
    props['P1476'].each { |o|
      value, lang = parse_literal(o)
      titles[lang] = value if value && lang
    }

    inception_start = props['P571'].filter_map { |o| parse_literal(o)&.first }.first
    inception_end = props['P572'].filter_map { |o| parse_literal(o)&.first }.first
    creators = props['P170'].filter_map { |o| qid_of(o) }

    {
      'qid' => qid,
      'instance_of' => instance_of,
      'label_en' => label_en,
      'label_ja' => label_ja,
      'titles' => titles,
      'creators' => creators,
      'inception_start' => inception_start,
      'inception_end' => inception_end,
      'image_url' => image_url,
    }
  end

  # ===================== フェーズ1: ダンプの抽出 =====================

  # lines (改行ごとに読み出せる何か。IO でも配列でもよい) を1エンティティずつ
  # 組み立て、対象条件を満たすものだけ [qid, record] として yield する。
  # curl|bzcat を経由しない配列を渡せるので、パース/組み立てロジックだけを
  # 単体テストできる (import 本体から切り出した理由はこれ)。
  # 戻り値は走査した実体数 (対象クラス外も含む)。
  def each_matched_record(lines, progress_every: 200_000)
    entity = Entity.new
    seen = 0
    matched = 0
    flush = lambda {
      next if entity.qid.nil?
      seen += 1
      record = build_record(entity.qid, entity.props, entity.label_en, entity.label_ja)
      if record
        matched += 1
        yield(entity.qid, record)
      end
      if progress_every && (seen % progress_every).zero?
        STDERR.print "\r#{Time.now.iso8601(0)}\t#{seen}件走査 / #{matched}件マッチ ..."
      end
    }

    lines.each { |raw|
      line = raw.chomp
      triple = split_triple(line)
      next unless triple
      subject, predicate, object_str = triple

      # subject (生URI) 同士で比較する。qid (短縮形) と比較すると型が
      # 揃わず常に不一致になり、1行ごとに flush されて props が絶対に
      # 蓄積されないバグになる (このヘルパを切り出す前に実際に踏んだ)。
      if subject != entity.subject
        flush.call
        entity = Entity.new
        entity.subject = subject
        entity.qid = qid_of("<#{subject}>") # Property/Lexeme等はqidがnilのまま=無視される
      end
      next unless entity.qid

      if predicate == LABEL_PRED
        value, lang = parse_literal(object_str) || [nil, nil]
        next unless value
        entity.label_en ||= value if lang == 'en'
        entity.label_ja ||= value if lang == 'ja'
      elsif predicate.start_with?(PROP_PREFIX)
        pid = predicate[PROP_PREFIX.length..]
        entity.props[pid] << object_str if WANTED_PROPS.include?(pid)
      end
    }
    flush.call
    seen
  end

  def import
    db = KVStore.new(LMDB_PATH)
    started = Time.now
    matched = 0
    seen = 0

    # set -o pipefail が無いと、bash のパイプはパイプ内で「最後に実行された
    # コマンド (bzcat)」の終了コードしか見ない。curl がダンプ取得の途中で
    # 失敗しても bzcat が (空/不完全な入力を) 0 で終えれば $?.success? が
    # true になり、実際には何も取れていないのに「完了」と表示してしまう。
    IO.popen(['bash', '-c', "set -o pipefail; curl -sS --fail '#{DUMP_URL}' | bzcat"]) { |io|
      seen = each_matched_record(io) { |qid, record|
        next if db.has?(qid) # 再実行時の重複書き込みを避ける (resumeの代わり)
        db[qid] = JSON.generate(record)
        matched += 1
      }
    }
    raise "curl|bzcat が失敗しました (exit #{$?.exitstatus})" unless $?.success?

    STDERR.puts "\r完了: #{seen}件走査 / #{matched}件マッチ (#{(Time.now - started).round}s)"
  ensure
    db&.close
  end

  # ===================== フェーズ2: 作者情報の補完 (ライブAPI) =====================
  #
  # ダンプ単体では P170 (creator) は参照先QIDしか分からない (生没年は別エンティティ)。
  # 抽出が終わったレコードが参照する creator QID の集合だけ、Wikidata API を
  # バッチ (最大50件/リクエスト) で叩いて名前・生没年を補う。
  # 429 (Too Many Requests) は Retry-After ヘッダーに従って待つ。

  WBGETENTITIES_URL = 'https://www.wikidata.org/w/api.php'
  BATCH_SIZE = 50
  REQUEST_INTERVAL_SEC = 30.0

  # Retry-After (秒数 or HTTP-date、RFC 9110 §10.2.3) を待機秒数にする。
  # "Wed, 21 Oct 2026 07:28:00 GMT" のような日付形式は String#to_i だと
  # 数字始まりでないため黙って 0 になり、レート制限中に即リトライしてしまう。
  def parse_retry_after(value)
    return 60 if value.nil? || value.strip.empty?
    return value.to_i if value.strip.match?(/\A\d+\z/)

    begin
      [(Time.httpdate(value) - Time.now).ceil, 0].max
    rescue ArgumentError
      60
    end
  end

  def http_get_json(uri, max_retries: 5)
    attempt = 0
    loop {
      req = Net::HTTP::Get.new(uri)
      req['User-Agent'] = USER_AGENT
      res = Net::HTTP.start(uri.host, uri.port, use_ssl: true) { |http| http.request(req) }

      case res
      when Net::HTTPTooManyRequests
        attempt += 1
        raise "429 が続きます (#{attempt}回目)" if attempt > max_retries
        wait = parse_retry_after(res['Retry-After'])
        STDERR.puts "\n429 Too Many Requests。Retry-After に従い #{wait}秒 待機します (#{attempt}/#{max_retries})"
        sleep wait
        next
      when Net::HTTPSuccess
        return JSON.parse(res.body)
      else
        raise "HTTP #{res.code}: #{res.message}"
      end
    }
  end

  # "+1452-04-15T00:00:00Z" 等 → 1452 (BCEは負数)。取れなければ nil。
  def wikidata_year(time_str)
    return nil unless time_str
    m = time_str.match(/\A([+-]?\d{1,6})-/)
    m && m[1].to_i
  end

  # LMDBへの書き込みはバッチ内の1件ずつ即コミットされ、次回起動時は
  # todo の算出で "author:#{qid}" 済みを弾くため、Ctrl+C で中断しても
  # 再度 `enrich` を叩くだけで取りこぼしなく再開できる。
  def enrich
    db = KVStore.new(LMDB_PATH)

    creator_qids = Set.new
    db.each { |_qid, json|
      record = JSON.parse(json)
      (record['creators'] || []).each { |c| creator_qids << c }
    }
    todo = creator_qids.reject { |c| db.has?("author:#{c}") }
    STDERR.puts "作者候補 #{creator_qids.size}件 (未取得 #{todo.size}件)"

    todo.to_a.each_slice(BATCH_SIZE) { |batch|
      sleep REQUEST_INTERVAL_SEC
      uri = URI(WBGETENTITIES_URL)
      uri.query = URI.encode_www_form(
        action: 'wbgetentities', ids: batch.join('|'),
        props: 'labels|claims', languages: 'en|ja', format: 'json'
      )
      body = http_get_json(uri)
      (body['entities'] || {}).each { |qid, entity|
        next if entity['missing']
        labels = entity['labels'] || {}
        birth = entity.dig('claims', 'P569', 0, 'mainsnak', 'datavalue', 'value', 'time')
        death = entity.dig('claims', 'P570', 0, 'mainsnak', 'datavalue', 'value', 'time')
        info = {
          'name_en' => labels.dig('en', 'value'),
          'name_ja' => labels.dig('ja', 'value'),
          'birth_year' => wikidata_year(birth),
          'death_year' => wikidata_year(death),
        }
        db["author:#{qid}"] = JSON.generate(info)
      }
      STDERR.print "\r#{Time.now.iso8601(0)}\t作者情報 #{batch.last}まで完了..."
    }
    STDERR.puts "\n完了"
  ensure
    db&.close
  end
end

if __FILE__ == $PROGRAM_NAME
  case ARGV[0]
  when nil then Wikimedia.import
  when 'enrich' then Wikimedia.enrich
  else abort "使い方: ruby wikimedia.rb [enrich]"
  end
end
