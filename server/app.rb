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

get '/v1/collection' do
  collections = Collection.all
  json collections.map { |collection|
    record = Image.find(collection.index_image_id)
    collection.attributes.merge({ image_url: IMAGE_STORE.getOrRetreive(record["id"], record["image_url"], "800").gsub(/^\./, ""), count: CollectionImage.where(collection_id: collection.id).count })
  }
end

def random(query)
  records = query.count
  random = Random.new(Time.now.to_i / 60 - (params['i'] || 0).to_i).rand(records)
  record = query.order(:id).limit(1).offset(random).first
  record["image_url"] = IMAGE_STORE.getOrRetreive(record["id"], record["image_url"], "max").gsub(/^\./, "")
  record
end

get '/v1/random/collection/:id' do
  json random(Image.eager_load(:collection_images).where("collection_images.collection_id": params['id']))
end

get '/v1/random/style/:style' do
  json random(Image.where("style like ?", "%#{params['style']}%"))

end

get '/v1/random' do
  json random(Image)
end

