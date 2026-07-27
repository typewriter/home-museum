require 'net/http'
require 'uri'
require 'json'
require 'set'
require_relative 'kv_store'
require 'time'

module Smithsonian
  BASE_URL = 'https://smithsonian-open-access.s3-us-west-2.amazonaws.com'

  # 全37ユニット(自然史標本・アーカイブ資料・図書館蔵書などを含む)のうち美術系のみに限定
  # saam: 米国美術館 / npg: 国立肖像画美術館 / hmsg: ハーシュホーン美術館彫刻庭園
  # fsg: 国立アジア美術館(Freer|Sackler) / chndm: クーパー・ヒューイット / nmafa: 国立アフリカ美術館
  UNIT_CODES = %w[saam npg hmsg fsg chndm nmafa]

  # 各ユニットのシャードファイルは数百に及ぶため、中断からの再開を
  # 完了済みシャードURLの記録で行う (resumptionTokenのような単一ポインタが存在しないため)
  RESUME_PATH = "#{File.dirname(__FILE__)}/smithsonian.resume"

  def import
    db = KVStore.new "#{File.dirname(__FILE__)}/smithsonian.lmdb"
    done = File.exist?(RESUME_PATH) ? File.readlines(RESUME_PATH, chomp: true).to_set : Set.new

    UNIT_CODES.each { |unit|
      shard_urls = fetch_lines("#{BASE_URL}/metadata/edan/#{unit}/index.txt")

      shard_urls.each { |shard_url|
        next if done.include?(shard_url)

        sleep 5

        response = Net::HTTP.get_response(URI.parse(shard_url))
        print "#{Time.now.iso8601(6)}\t"
        print "#{shard_url}...\t"

        if response.code != "200" || !response.body
          puts "error! (status code: #{response.code})"
          next
        end

        count = 0
        response.body.each_line { |line|
          line = line.strip
          next if line.empty?

          record = JSON.parse(line)
          id = record.dig("content", "descriptiveNonRepeating", "record_ID") || record["id"]
          next if !id

          db[id] = line
          count += 1
        }
        puts "ok. (#{count} records)"

        File.open(RESUME_PATH, "a") { |f| f.puts shard_url }
      }
    }
  end

  def fetch_lines(url)
    sleep 5
    response = Net::HTTP.get_response(URI.parse(url))
    response.body.to_s.each_line.map(&:strip).reject(&:empty?)
  end
  module_function :import, :fetch_lines
end

Smithsonian.import
