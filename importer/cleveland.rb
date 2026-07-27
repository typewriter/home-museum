require 'net/http'
require 'uri'
require 'json'
require_relative 'kv_store'
require 'time'

module Cleveland
  BASE_URL = 'https://openaccess-api.clevelandart.org/api/artworks/'
  LIMIT = 100

  # resumptionToken のような単一ポインタが無く skip/limit のみのため、
  # 完了済み skip 位置を自前で永続化して中断・再開に対応する
  RESUME_PATH = "#{File.dirname(__FILE__)}/cleveland.resume"

  def import
    db = KVStore.new "#{File.dirname(__FILE__)}/cleveland.lmdb"
    skip = File.exist?(RESUME_PATH) ? File.read(RESUME_PATH).to_i : 0

    loop {
      sleep rand(5..10)

      url = "#{BASE_URL}?limit=#{LIMIT}&skip=#{skip}"
      response = Net::HTTP.get_response(URI.parse(url))
      json = JSON.parse(response.body)

      total = json.dig("info", "total")
      print "#{Time.now.iso8601(6)}\t"
      print "#{skip}/#{total}...\t"

      artworks = json["data"]
      artworks.each { |artwork|
        db[artwork["id"].to_s] = JSON.generate(artwork)
      }
      puts "ok. (#{artworks.size} records)"

      skip += LIMIT
      File.write(RESUME_PATH, skip.to_s)
      break if artworks.empty? || skip >= total
    }

    File.delete(RESUME_PATH) if File.exist?(RESUME_PATH)
  end
  module_function :import
end

Cleveland.import
