#!/usr/bin/env ruby

require "sinatra"
require "sinatra/json"
require "sqlite3"
require "RMagick"
require_relative "./image_store.rb"

DATABASE_PATH = ENV['DATABASE_PATH'] || "./hm.db"
DB = SQLite3::Database.new(DATABASE_PATH, { readonly: true })
DB.results_as_hash = true

IMAGE_PATH  = ENV['IMAGE_PATH'] || "./public/images"
IMAGE_STORE = ImageStore.new(IMAGE_PATH)

get '/v1' do
  "Hello world!"
end

get '/v1/random/:style?' do
  records = 0
  if params['style']
    records = DB.get_first_value("select count(*) from images where style like ?", "%#{params['style']}%").to_i
  else
    records = DB.get_first_value("select count(*) from images").to_i
  end

  random = Random.new(Time.now.to_i / 60 - (params['i'] || 0).to_i).rand(records)
  #random = Random.new.rand(records)

  record = nil
  if params['style']
    query = <<-"SQL"
      select * from images where style like ? order by id limit 1 offset #{random}
    SQL
    record = DB.get_first_row(query, "%#{params['style']}%")
  else
    query = <<-"SQL"
      select * from images order by id limit 1 offset #{random}
    SQL
    record = DB.get_first_row(query)
  end

  # get image
  record["image_url"] = IMAGE_STORE.getOrRetreive(record["id"], record["image_url"], "max").gsub(/\.\/public/, "")
  json record
end

