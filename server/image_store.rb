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
      open(image_url) do |image|
        open(filename, "w+b") do |file|
          file.write(image.read)
        end
      end
    end

    # RESIZE if not exists
    # FIXME: not supported

    # RETURN FILE PATH
    filename
  end
end
