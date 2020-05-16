#!/usr/bin/env ruby

require "bundler/setup"
require "active_record"
Bundler.require

require_relative "./image_store.rb"

DATABASE_PATH = ENV['DATABASE_PATH'] || "./hm.db"
# DB = SQLite3::Database.new(DATABASE_PATH, { readonly: true })
# DB.results_as_hash = true

IMAGE_PATH  = ENV['IMAGE_PATH'] || "./public/images"
IMAGE_STORE = ImageStore.new(IMAGE_PATH)

ActiveRecord::Base.establish_connection(
  adapter: 'sqlite3',
  database: DATABASE_PATH
)

class Image < ActiveRecord::Base
  has_many :collection_images
end

class Collection < ActiveRecord::Base
  has_many :collection_images
  has_many :images, through: :collection_images
end

class CollectionImage < ActiveRecord::Base
  belongs_to :collection
  belongs_to :image
end

get '/v1' do
  "Hello world!"
end

get '/v1/random/:style?' do
  records = 0
  if params['style']
    records = Image.where("style like ?", "%#{params['style']}%").count
  else
    records = Image.count
  end

  random = Random.new(Time.now.to_i / 60 - (params['i'] || 0).to_i).rand(records)
  #random = Random.new.rand(records)

  record = nil
  if params['style']
    record = Image.where("style like ?", "%#{params['style']}%").order(:id).limit(1).offset(random).first
  else
    record = Image.order(:id).limit(1).offset(random).first
  end

  # get image
  record["image_url"] = IMAGE_STORE.getOrRetreive(record["id"], record["image_url"], "max").gsub(/^\./, "")
  json record
end

