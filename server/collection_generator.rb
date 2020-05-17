#!/usr/bin/env ruby

require_relative "./app.rb"

author = "typewriter"

generate_collections = [
  { title: "浮世絵",
    index_art: "Numazu, Number 13, from the series Fifty-Three Stations",
    artists: [
      "Adachi Ginko",
      "Angyusai Enshi",
      "Cho%sai Ei", # CUT
      "Eishosai Choki",
      "Enkyo",
      "Furuyama Bansui",
      "Furuyama Moro", # CUT
      "Hanegawa Chincho", # Typo
      "Hanekawa Wagen",
      "Hashimoto%Chikanobu",
      "Hishikawa Masa", # CUT
      "Hishikawa Moro", # CUT
      "Hishikawa Tomo", # CUT
      "Ichimei",
      "Ichirakutei Eisui",
      "Ippitsusai Bunch",
      "Ishikawa Toyonobu",
      "Isoda Koryusai",
      "Katsukawa Shun", # CUT
      "Katsushika", # CUT
      "Kawanabe Kyo", # CUT
      "Keisai Eisen",
      "Keisen Eisen",
      "Kishi Bunsho",
      "Kitagawa Utamaro",
      "Kitao Masa", # CUT
      "Kitao Shige", # CUT
      "Kobayashi Ikuhide",
      "Kobayashi Kiyochika",
      "Komai Yoshinobu",
      "Kikugawa Eishin",
      "Kondo Kiyo", # CUT
      "Kondo %nobu",
      "Kubo Shunman",
      "Mangetsudo",
      "Masunobu",
      "Matsukawa Hanzan",
      "Migita Toshihide",
      "Mihata Joryu",
      "Mitsunobu",
      "Miyagawa Ch", # CUT
      "Miyagawa Shunsui",
      "Miyagawa %nobu",
      "Mizuno Toshikata",
      "Naka Kuninobu",
      "Nishikawa Suke", # CUT
      "Nishimura Shige", # CUT
      "Niwa Tokei",
      "Okumura %nobu",
      "Okumura Masa", # CUT
      "Ochiai Yoshi", # CUT
      "Ogata Gekko",
      "Ogata Kenzan",
      "Sesshu",
      "Shiba Ko", # CUT
      "Shichu",
      "Shiko",
      "Shoju",
      "Shotei Hokuju",
      "Sugimura Jihei",
      "Suzuki Haru", # CUT
      "Tachibana Minko",
      "Taguchi Beisaku",
      "Tamagawa Shu", # CUT
      "Teisai Hokuba",
      "Torii Kiyo", # CUT
      "Toriyama Sekien",
      "Tomikawa Fusanobu",
      "Tosend%Rif",
      "Toshusai Sharaku",
      "Toyohara Kunichika",
      "Tsuchiya Koitsu",
      "Tsukioka Kogyo",
      "Tsukioka Yoshitoshi",
      "Tsunekawa Shigenobu",
      "Uchimasa",
      "Utagawa", # CUT
      "Yanagawa Kuninao",
      "Yanagawa Shige", # CUT
      "Yamamoto %nobu",
      "Yosai Nobukazu",
      "Ran-u",
      "Rekisentei Eiri",
      "Ryuryuko Shinsai",
    ]
  },
  {
    title: "日本画・南画",
    index_art: "Famous Themes for Painting Study Known as %The Garden of Painting",
    artists: [
      "Ike Taiga", # Typo
      "Ikeno Taiga",
      "Taigado",
      "Taikado",
      "Kasho",
      "Yanagisawa Kien",
      "Ryurikyo",
      "Sakaki Hyakusen",
      "Gion Nankai",
      "Uragami Shu", # CUT
      "Aoki Shukuya",
      "Urakami Gyokudo",
      "Ikeno Gyokuran",
      "Ike Gyokuran",
      "Kuwayama Gyokushu",
      "Noro Kaiseki",
      "Kumasaka Tekizan",
      "Goshun",
      "Yosa Buson",
      "Nakabayashi Chiku", # CUT
      "Noguchi Syo",
      "Hine Taizan",
      "Nukina S%ou",
      "Nukina Kaioku",
      "Tanomura Chokunyu",
      "Araki Ka%po",
      "Komuro Suiun",
      "Tazaki So%un",
      "Haruki Nanko",
      "Suzuki %uyo",
      "Tani Buncho",
      "Matsubayashi Keigetsu",
      "Okuhara Seiko",
      "Noguchi Y%koku",
      "Tsubaki Chinzan",
      "Watanabe Kazan",
      "Hoashi Kyo", # CUT
      "Takahashi S%hei", # CUT
      "Tanomura Chikuden",
      "Hazama Seigai",
      "Suzuki Hyakunen",
      "Takashima Hokkai",
      "Yamaoka Beika",
      "Kino Baitei",
      "kazan",
      # 南画
      # ------
      # 日本画
      "Kano ",
      "Sesshu",
      "Soami",
      "Souami",
      "Sesson",
      "Sotan",
      "Soutan",
      "Tosa Mitsu", # CUT
      "Kaih% Yu", # CUT
      "Soga Chokuan",
      "Hasegawa To", # CUT
      "Ito Jakuch",
      "Tawaraya So", # CUT
      "Nagasawa Rosetsu",
      "Nishiyama Ho", # CUT
      "Hasegawa %nobu",
      "Hasegawa Sada", # CUT
      "Hasegawa Se", # CUT
      "Ogata Ko", # CUT
      "Ogata Kan", # CUT
      "Ohara Koson",
      "Obara Koson",
      "Kaburaki Kiyotaka",
      "Kawase Hasui",
    ]
  },
  { title: "バルビゾン派・印象派",
    index_art: "Stacks of Wheat (End of Summer)",
    artists: [
      "Camille Pissarro",
      "Pissarro%Camille",
      "Edgar de Gas",
      "Alfred Sisley",
      "Sisley%Alfred",
      "Paul C_zanne",
      "C_zanne%Paul",
      "Claude Monet",
      "Monet%Claude",
      "Berthe Morisot",
      "Morisot%Berthe",
      "Pierre%Auguste%Renoir",
      "Renoir%Pierre%Auguste",
      "Armand Guillaumin",
      "Guillaumin%Armand",
      "Mary%Cassatt",
      "Cassatt%Mary",
      "Gustave Caillebotte",
      "Caillebotte%Gustave",
      "Eva Gonzal_s",
      "Gonzal_s%Eva",
      "Jean%Baptiste%Camille Corot",
      "Corot%Jean%Baptiste%Camille",
      "Jean-Fran_ois Millet",
      "Millet%Jean-Fran_ois",
      "Th_odore Rousseau",
      "Rousseau%Th_odore",
      "Constant Troyon",
      "Troyon%Constant",
      "Narcisse%D_az%de%la%Pe_a",
      "D_az%de%la%Pe_a%Narcisse",
      "Jules Dupr_",
      "Dupr_%Jules",
      "Charles%Fran_ois Daubigny",
      "Daubigny%Charles%Fran_ois",
    ]
  },
  { title: "バロック絵画の時代",
    index_art: "Wolf and Fox Hunt",
    artists: [
      "Frans Hals",
      "Rembrandt Harmenszoon",
      "Rembrandt%van Rijn",
      "Jan Steen",
      "Steen, Jan",
      "Jacob%van%Ruisdael",
      "Ruisdael%Jacob",
      "Johannes Vermeer",
      "Vermeer%Johannes",
      "Hollar, V_clav",
      "V_clav Hollar",
      "Karel _kr_ta",
      "_kr_ta, Karel",
      "Petr Brandl",
      "Brandl Petr",
      "Wenzel%Reiner",
      "Reiner, Wenzel",
      "Jan Brueghel",
      "Paul Rubens",
      "Rubens, Pierre",
      "Rubens, Peter",
      "Frans Sn",
      "Jacob Jordaens",
      "Jordaens, Jacob",
      "Anthony%Dyck",
      "David Teniers",
      "Teniers, David",
      "Jean de Beaugrand",
      "Le Nain",
      "Georges de La Tour",
      "Poussin, Nicolas",
      "Nicolas Poussin",
      "Claude Lorrain",
      "Hyacinthe Rigaud",
      "Rigaud, Hyacinthe",
      "Andrea Pozzo",
      "L_dovico Carracci",
      "Agostino Carracci",
      "Annibale Carracci",
      "Carracci, Annibale",
      "Michelangelo%Caravaggio",
      "Caravaggio%Michelangelo",
      "Guido Reni",
      "Reni, Guido",
      "Giovanni%Barbieri",
      "Guercino",
      "Artemisia Gentileschi",
      "Pietro da Cortona",
      "Pietro Berrettini",
      "Pietro Berettini",
      "Salvator Rosa",
      "Giovanni Battista Tiepolo",
      "Giambattista Tiepolo",
      "Juan de Vald_s Leal",
      "Alonso Cano",
      "Jos_ de Ribera",
      "Jusepe de Ribera",
      "Francisco de Zurbar_n",
      "Diego Rodr_guez",
      "Diego Vel_zquez",
      "Bartolom_%Murillo",
    ]
  }
]

generate_collections.each { |col|
  ActiveRecord::Base.transaction do
    collection = Collection.find_or_initialize_by(title: col[:title])

    image = Image.where("title like ?", "%#{col[:index_art]}%").first
    if !image
      raise Error.new("image not found")
    end

    collection.attributes = { title: col[:title], author: author, index_image_id: image.id }
    collection.save!

    CollectionImage.where(collection_id: collection.id).delete_all
    image_ids = col[:artists].map { |artist| Image.where("artist like ?", "%#{artist}%").pluck(:id) }.flatten.uniq
    image_ids.each { |image_id|
      CollectionImage.create!({ collection_id: collection.id, image_id: image_id })
    }

    STDERR.puts "Collection #{col[:title]}: #{CollectionImage.where(collection_id: collection.id).count} images."
  end
}

