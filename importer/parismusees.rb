require 'net/http'
require 'uri'
require 'json'
require 'leveldb'
require 'time'

module ParisMusees
  def generate_query(offset)
    <<-"EOF"
{
  nodeQuery(
    filter: {conditions:[
     {field: "type", value: "oeuvre"},
    ]}
     sort: [
      {field: "created", direction: ASC}
    ]
    limit: 100
    offset: #{offset}
  ) {
    count
    entities {
      entityId
      ... on NodeOeuvre {
        title
        absolutePath
        fieldUrlAlias
        fieldTitreDeMediation
        fieldSousTitreDeMediation
        fieldOeuvreAuteurs {
          entity {
            fieldAuteurAuteur {
              entity {
                name
                fieldPipDateNaissance {
                  startYear
                }
                fieldPipLieuNaissance
                fieldPipDateDeces {
                  startYear
                }
                 fieldLieuDeces
              }
            }
            fieldAuteurFonction {
              entity {
                name
              }
            }

          }
        }
        fieldVisuelsPrincipals {
          entity {
            name
            vignette
            publicUrl
          }
        }
        fieldDateProduction {
          startPrecision
          startYear
          startMonth
          startDay
          sort
          endPrecision
          endYear
          endMonth
          endDay
          century
        }
        fieldOeuvreSiecle {
           entity {
            name
          }
        }
        fieldOeuvreTypesObjet {
          entity {
            name
            fieldLrefAdlib
          }
        }
        fieldDenominations {
          entity {
            name
          }
        }
        fieldMateriauxTechnique{
          entity {
            name
          }
        }
        fieldOeuvreDimensions {
          entity {
            fieldDimensionPartie {
              entity {
                name
              }
            }
            fieldDimensionType {
              entity {
                name
              }
            }
            fieldDimensionValeur
            fieldDimensionUnite {
             entity {
                name
              }
            }
          }
        }
        fieldOeuvreInscriptions{
          entity {
            fieldInscriptionType {
              entity {
                name
              }
            }
            fieldInscriptionMarque {
              value
            }
            fieldInscriptionEcriture {
              entity {
                name
              }
            }
          }
        }
        fieldOeuvreDescriptionIcono {
          value
        }
        fieldCommentaireHistorique {
          value

        }
        fieldOeuvreThemeRepresente	 {
          entity {
            name
          }
        }
        fieldLieuxConcernes {
          entity {
            name
          }
        }
        fieldModaliteAcquisition {
          entity {
            name
          }
        }
        fieldDonateurs {
          entity {
            name
          }
        }
        fieldDateAcquisition {
          startPrecision
          startYear
          startMonth
          startDay
          sort
          endPrecision
          endYear
          endMonth
          endDay
          century
        }
        fieldOeuvreNumInventaire
        fieldOeuvreStyleMouvement {
          entity {
            name
          }
        }
        fieldMusee {
          entity {
            name
          }
        }
        fieldOeuvreExpose {
          entity {
            name
          }
        }
        fieldOeuvreAudios {
          entity {
            fieldMediaFile {
              entity {
                url
                uri {
                  value
                  url
                }
              }
            }
          }
        }
        fieldOeuvreVideos {
          entity {
            fieldMediaVideoEmbedField
          }
        }
        fieldHdVisuel {
          entity {
            fieldMediaImage {
              entity {
                url
              }
            }
          }
        }
      }
    }
  }
}
    EOF
  end

  def import
    graphql_endpoint = "http://apicollections.parismusees.paris.fr/graphql"
    token = ENV['TOKEN']

    db = LevelDB::DB.new "#{File.dirname(__FILE__)}/parismusees.leveldb"
    offset = 0
    loop {
      sleep 10

      uri = URI.parse(graphql_endpoint)
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"

      headers = { "Content-type": "application/json", "auth-token": token }
      query = ParisMusees.generate_query(offset)

      response = http.post(uri.path, { "query": query }.to_json, headers)
      json = JSON.parse(response.body)

      print "#{Time.now.iso8601(6)}\t#{offset}/#{json.dig("data","nodeQuery","count")}"

      artworks = json.dig("data", "nodeQuery", "entities")
      if !artworks || artworks.size == 0
        break
      end

      artworks.compact.each { |artwork|
        db[artwork["entityId"].to_s] = JSON.generate(artwork)
      }

      offset += 100
    }
  end
  module_function :import
  module_function :generate_query
end

ParisMusees.import

