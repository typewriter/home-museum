#!/usr/bin/env ruby

require "sqlite3"

DATABASE_PATH = ENV['DATABASE_PATH'] || "./hm.db"
db = SQLite3::Database.new DATABASE_PATH

sql = <<-SQL
  CREATE TABLE IF NOT EXISTS images (
    id integer primary key,
    source text,
    category text,
    style text,
    title text,
    artist text,
    date text,
    medium text,
    origin text,
    dimensions text,
    credit text,
    description text,
    source_url text,
    image_url text
  );
  CREATE INDEX IF NOT EXISTS images_style ON images (style);
  CREATE UNIQUE INDEX IF NOT EXISTS images_source ON images (source_url);

  CREATE TABLE IF NOT EXISTS collections (
    id integer primary key,
    title text,
    author text,
    description text,
    index_image_id text,
    tag text
  );

  CREATE TABLE IF NOT EXISTS collection_images (
    id integer primary key,
    collection_id integer,
    image_id integer
  );

  CREATE INDEX IF NOT EXISTS collection_images_collection ON collection_images (collection_id);
SQL
db.execute_batch(sql)
db.close
