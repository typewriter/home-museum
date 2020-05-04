require 'net/http'
require 'uri'
require 'json'
require 'leveldb'
require 'time'

module Met
  def import
    response = Net::HTTP.get_response(URI.parse('https://collectionapi.metmuseum.org/public/collection/v1/objects'))
    json = JSON.parse(response.body)

    db = LevelDB::DB.new "#{File.dirname(__FILE__)}/met.leveldb"
    json["objectIDs"].each { |object_id|
      id = object_id.to_s
      next if db.includes?(id)

      print "#{Time.now.iso8601(6)}\t"
      print "#{id}...\t"
      sleep 0.1

      response = Net::HTTP.get_response(URI.parse("https://collectionapi.metmuseum.org/public/collection/v1/objects/#{id}"))
      if response.code == "200" && response.body
        db[id] = response.body
        puts "ok."
      else
        puts "error! (status code: #{response.code})"
      end
    }
  end
  module_function :import
end

Met.import

