require 'net/http'
require 'uri'
require 'json'
require_relative 'kv_store'
require 'time'

module Met
  OPEN_TIMEOUT = 10
  READ_TIMEOUT = 15

  def get(url)
    uri = URI.parse(url)
    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = uri.scheme == 'https'
    http.open_timeout = OPEN_TIMEOUT
    http.read_timeout = READ_TIMEOUT
    http.max_retries = 0 # 既定の 1 回再試行だとハングするレコードで待ち時間が倍になる
    http.request(Net::HTTP::Get.new(uri))
  end
  module_function :get

  def import
    response = get('https://collectionapi.metmuseum.org/public/collection/v1/objects')
    json = JSON.parse(response.body)

    db = KVStore.new "#{File.dirname(__FILE__)}/met.lmdb"
    json["objectIDs"].each { |object_id|
      id = object_id.to_s
      next if db.has?(id)

      print "#{Time.now.iso8601(6)}\t"
      print "#{id}...\t"
      sleep 0.5

      begin
        response = get("https://collectionapi.metmuseum.org/public/collection/v1/objects/#{id}")
      rescue StandardError => e
        # 応答を返さないレコードが存在する (例: 327710)。LMDB に書かなければ
        # API 復旧後の再実行で拾い直せるので、ここでは飛ばして先に進む。
        puts "error! (#{e.class}: #{e.message})"
        next
      end

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

