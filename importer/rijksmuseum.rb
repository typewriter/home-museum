require 'net/http'
require 'uri'
require 'json'
require 'rexml/document'
require_relative 'kv_store'
require 'time'

module Rijksmuseum
  BASE_URL = 'https://data.rijksmuseum.nl/oai'

  # ListRecords は 1 リクエストあたり 50 件・全体で 80 万件超と規模が大きく、
  # 中断からの再開を resumptionToken の再送信で行うため別ファイルに永続化する
  RESUME_PATH = "#{File.dirname(__FILE__)}/rijksmuseum.resume"

  def import
    db = KVStore.new "#{File.dirname(__FILE__)}/rijksmuseum.lmdb"

    url = if File.exist?(RESUME_PATH)
            "#{BASE_URL}?verb=ListRecords&resumptionToken=#{URI.encode_www_form_component(File.read(RESUME_PATH))}"
          else
            "#{BASE_URL}?verb=ListRecords&metadataPrefix=edm"
          end

    loop {
      sleep 5

      response = Net::HTTP.get_response(URI.parse(url))
      doc = REXML::Document.new(response.body)

      if (error = doc.get_elements("//error").first)
        print "#{Time.now.iso8601(6)}\t"
        puts "error: #{error.attributes["code"]} #{error.text}"

        # resumptionToken の期限切れ等は最初からやり直す
        if error.attributes["code"] == "badResumptionToken"
          File.delete(RESUME_PATH) if File.exist?(RESUME_PATH)
          url = "#{BASE_URL}?verb=ListRecords&metadataPrefix=edm"
          next
        end
        break
      end

      records = doc.get_elements("//record")
      records.each { |record|
        header = record.get_elements("header").first
        next if header.attributes["status"] == "deleted"

        identifier = header.get_text("identifier").to_s
        id = identifier.split("/").last

        rdf = record.get_elements("metadata/rdf:RDF").first
        next if !rdf

        db[id] = JSON.generate({
          identifier: identifier,
          datestamp: header.get_text("datestamp").to_s,
          rdf_xml: rdf.to_s,
        })
      }

      resumption_token = doc.get_elements("//resumptionToken").first
      first_id = records.first&.get_text("header/identifier")
      last_id = records.last&.get_text("header/identifier")

      print "#{Time.now.iso8601(6)}\t"
      print "#{records.size} records [#{first_id} .. #{last_id}] (total #{resumption_token&.attributes&.[]("completeListSize")})...\n"

      token = resumption_token&.text
      if !token || token.empty?
        File.delete(RESUME_PATH) if File.exist?(RESUME_PATH)
        break
      end

      File.write(RESUME_PATH, token)
      url = "#{BASE_URL}?verb=ListRecords&resumptionToken=#{URI.encode_www_form_component(token)}"
    }
  end
  module_function :import
end

Rijksmuseum.import
