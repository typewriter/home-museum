<template>
  <div style="width: 100vw; height: 100vh;" class="uk-light uk-flex uk-flex-center uk-flex-middle uk-text-small">
    <transition name="fade" mode="out-in">
      <div id="loading" key="loading" v-if="loading">
        <div uk-spinner="ratio: 3"></div>
        <br>{{ loadingMessage }}<br><small>{{ $t("random.dependNetwork") }}</small>
      </div>
      <div v-else key="display">
        <transition name="fade" mode="out-in" appear>
          <div id="even" key="even" v-if="even" style="width: 100vw; height: 100vh;" class="uk-background-contain" v-bind:style="{ backgroundImage: evenImage }"><!-- :style="{ backgroundImage: 'url(' + require('@/assets/imgs/monet/stacksofwheat-endofsummer.jpg') + ')' }"> -->
            <transition name="fade">
              <div v-if="fullscreenTip" v-on:click="fullscreenTip = false" class="uk-overlay uk-padding-small uk-overlay-primary uk-position-center uk-position-small">{{ $t("random.switchInterval") }}<br><br>{{ $t("random.enterFullscreen") }}</div>
            </transition>
            <transition name="fade">
              <div v-if="description" class="uk-overlay uk-padding-small uk-overlay-primary uk-position-bottom-left uk-position-small uk-text-left" v-html="evenDescription"></div>
            </transition>
          </div>
          <div id="odd" key="odd" v-else style="width: 100vw; height: 100vh;" class="uk-background-contain" v-bind:style="{ backgroundImage: oddImage }"><!-- " :style="{ backgroundImage: 'url(' + require('@/assets/imgs/monet/waterlilies.jpg') + ')' }"> -->
            <transition name="fade">
              <div v-if="description" class="uk-overlay uk-padding-small uk-overlay-primary uk-position-bottom-left uk-position-small uk-text-left" v-html="oddDescription"></div>
            </transition>
          </div>
        </transition>
      </div>
    </transition>
  </div>
</template>

<script lang="ts">
import Vue from 'vue'
export default Vue.extend({
  name: 'Random',
  data: function() { return {
    server: "",
    apiPath: "/v1/random",  // APIエンドポイント
    loading: true,       // 初期ロード
    loadingMessage: this.$t("random.retreiveArtworks"),
    fullscreenTip: true, // フルスクリーン説明
    even: true,          // どちらを表示しているか
    description: true,   // 説明表示
    evenImage: "",       // イメージアドレス
    evenDescription: "",
    oddImage: "",        // イメージアドレス
    oddDescription: "",
    // ロード中、切替時間などを可変とする予定である
  }},
  methods: {
    switch() {
      this.description = true
      this.even = !this.even
      this.fetchNextArtwork()
    },
    initialLoad() {
      this.updateLang(this.$route.params.lang)
      this.loadingMessage = this.$t("random.retreiveArtworks")
      fetch(this.server + this.apiPath + (this.$route.params.type ? `/${this.$route.params.type}/${this.$route.params.name}` : "") + "?i=1")
        .then(function(response) {
          return response.json();
        }).then(json => {
          const imageUrl = this.server + json["image_url"]
          const description = `${json["artist"] || ""}<br><strong>${json["title"] || ""}</strong><br><small>${json["date"] || ""}<br>${json["medium"] || ""}<br>${json["credit"] || ""}</small><!-- ${json["description"] || ""} -->`
          this.evenImage = `url("${imageUrl}")`
          this.evenDescription = description
          this.loadingMessage = this.$t("random.retreiveArtworksImage")

          // duplicate load
          const img = new Image()
          img.onload = () => {
            this.loading = false
            this.fetchNextArtwork()
            setTimeout(() => this.fullscreenTip = false, 10000)
          }
          img.src = imageUrl
        })
    },
    fetchNextArtwork() {
      // fetch next artwork
      fetch(this.server + this.apiPath + (this.$route.params.type ? `/${this.$route.params.type}/${this.$route.params.name}` : ""))
        .then(function(response) {
          return response.json();
        }).then(json => {
          const imageUrl = this.server + json["image_url"]
          const description = `${json["artist"] || ""}<br><strong>${json["title"] || ""}</strong><br><small>${json["date"] || ""}<br>${json["medium"] || ""}<br>${json["credit"] || ""}</small><!-- ${json["description"] || ""} -->`
          if (this.even) {
            this.oddImage = `url("${imageUrl}")`
            this.oddDescription = description
          } else {
            this.evenImage = `url("${imageUrl}")`
            this.evenDescription = description
          }

          // preload
          const img = new Image()
          img.src = imageUrl
        })
      setTimeout(() => this.description = false, 30000)
      setTimeout(this.switch, 60000)
    },
    toggleFullscreen() {
      this.fullscreenTip = false
      if (!document.fullscreenElement) {
        document.documentElement.requestFullscreen()
        return
      }
      if (document.exitFullscreen) {
        document.exitFullscreen();
      }
    },
    updateLang(lang: any) {
      if (!lang) {
        lang = "ja"
      }
      this.$i18n.locale = lang
      document.title = this.$t("random.pageTitle") as string
    },
  },
  mounted() {
    document.addEventListener("keypress", (e) => {
      if (e.keyCode === 13) {
        this.toggleFullscreen()
      }
    }, false)
    this.initialLoad()
  },
  watch: {
    '$route' (to, from) {
      this.updateLang(to.params.lang)
    }
  },
})
</script>

<style lang="scss" scoped>
.fade-enter-active, .fade-leave-active {
  transition: opacity 2s;
}
.fade-enter, .fade-leave-to {
  opacity: 0;
}
</style>