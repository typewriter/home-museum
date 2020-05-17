<template>
  <div>
    <div id="nav" class="uk-padding-remove">
      <router-link to="/en/">English</router-link> |
      <router-link to="/">日本語</router-link>
    </div>
    <div class="home uk-light uk-background-secondary">
      <div class="uk-margin uk-height-medium uk-flex uk-flex-middle uk-flex-center uk-background-cover uk-background-bottom-center" style="background-image: url('/waterlilies.jpg')">
        <div>
        <h1 class="uk-text-center uk-animation-fade uk-overlay-primary">{{ $t("home.title") }}<br><small>{{ $t("home.subtitle") }}</small></h1>
        <br>
        <span class="uk-overlay-primary">{{ $t("home.description") }}</span>
        </div>
      </div>
      <div class="uk-padding uk-animation-fade">
        <div>
          <h2>{{ $t("home.collection") }}</h2>
        </div>
        <div class="uk-child-width-1-2@s uk-child-width-1-3@l" uk-grid v-if="collections.length != 0">
          <div v-for="item in collections" :key="item['id']">
            <a :href="'random' + (item['id'] == 9999 ? '' : '/collection/' + item['id'])">
            <div class="uk-card uk-card-default uk-card-large">
              <div class="uk-card-media-top uk-height-medium uk-flex uk-flex-middle uk-flex-center">
                <img :src="item['image_url']" class="uk-responsive-height uk-responsive-width">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">{{ item['title'] }}</h3>
                <p>{{ $t("home.number") }}{{ item['count'] }}</p>
              </div>
            </div>
            </a>
          </div>
        </div>
        <div v-else>
          {{ $t("home.loading") }}
        </div>
      </div>
    </div>
    <div id="footer" class="uk-light uk-text-small uk-text-light">
      {{ $t("home.claim") }}<br>
      Developer: {{ $t("home.developer")}} (<a href="https://nyamikan.net/">{{ $t("home.nyamikan") }}</a>)
    </div>
  </div>
</template>

<script>
export default {
  name: "Home",
  data: () => { return {
  server: "",
    apiPath: "/v1/collection",
    collections: [],
  }},
  mounted() {
    this.updateLang(this.$route.params.lang)
    fetch(this.server + this.apiPath)
      .then(function(response) {
        return response.json()
      }).then(json => {
        // eslint-disable-next-line @typescript-eslint/camelcase
        this.collections = json.concat([{ id: 9999, title: "ランダム (Random)", count: "400000~", image_url: "/vasewithloophandles.jpg" }])
      })
  },
  watch: {
    '$route' (to, from) {
      this.updateLang(to.params.lang)
    }
  },
  methods: {
    updateLang(lang) {
      if (!lang) {
        lang = "ja"
      }
      this.$i18n.locale = lang
      document.title = this.$t("home.pageTitle")
    },
  }
};
</script>

<style lang="scss" scoped>

</style>
