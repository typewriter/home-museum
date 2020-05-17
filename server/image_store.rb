require 'open-uri'

class ImageStore
  def initialize(path)
    @path = path
  end

  def getOrRetreive(id, image_url, size)
    filename = File.join(@path, "#{id}.jpg")

    # GET if not exists
    if !File.exists?(filename)
      STDERR.puts "Retreive: #{image_url} (save to #{filename})"
      begin
        open(image_url) do |image|
          open(filename, "w+b") do |file|
            file.write(image.read)
          end
        end
      rescue OpenURI::HTTPError => ex
        if ex.io.status.first == "308"
          image_url = ex.io.meta["location"]
          retry
        end
        raise ex
      end
    end

    if size == "max"
      return filename
    end

    resized_filename = File.join(@path, "#{id}.#{size}.jpg")
    if File.exist?(resized_filename)
      return resized_filename
    end

    original = Magick::Image.read(filename).first
    image = original.resize_to_fit(size)
    image.write(resized_filename) {
      self.quality = 80
    }

    return resized_filename
  end
end
