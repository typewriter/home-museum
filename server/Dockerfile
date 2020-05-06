FROM ruby:2.7-slim

RUN apt-get update && apt-get install -y pkg-config libmagickcore-dev build-essential
RUN apt-get update && apt-get install -y libsqlite3-dev
WORKDIR /app
COPY Gemfile Gemfile.lock ./
RUN bundle install
COPY Gemfile Gemfile.lock app.rb image_store.rb config.ru ./
EXPOSE 8080
ENV TZ=Asia/Tokyo
CMD ["bundle", "exec", "rackup", "--host", "0.0.0.0", "--port", "8080"]

