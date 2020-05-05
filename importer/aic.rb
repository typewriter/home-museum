require 'net/http'
require 'uri'
require 'json'
require 'leveldb'
require 'time'

module AIC
  def import
    url = "https://aggregator-data.artic.edu/api/v1/artworks?limit=100"

    db = LevelDB::DB.new "#{File.dirname(__FILE__)}/aic.leveldb"

    loop {
      sleep 10

      response = Net::HTTP.get_response(URI.parse(url))
      json = JSON.parse(response.body)

      print "#{Time.now.iso8601(6)}\t"
      print "#{json.dig("pagination", "current_page")}/#{json.dig("pagination", "total_pages")}...\t"

      artworks = json["data"]
      artworks.each { |artwork|
        db[artwork["id"].to_s] = JSON.generate(artwork)
      }

      if !(url = json.dig("pagination", "next_url"))
        break
      end
    }
  end
  module_function :import
end

AIC.import

