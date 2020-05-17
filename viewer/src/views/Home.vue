<template>
  <div>
    <!--
    <div id="nav">
      人気のコレクション(準備中) |
      <router-link to="/prototype/random/style/japan">日本画</router-link> |
      <router-link to="/prototype/random/style/impress">印象派</router-link> |
      西洋画(準備中) |
      陶芸作品(準備中) |
      <router-link to="/prototype/random">ランダム</router-link>
    </div>
    -->
    <div class="home uk-light uk-background-secondary">
      <div class="uk-margin uk-height-medium uk-flex uk-flex-middle uk-flex-center uk-background-cover uk-background-bottom-center" style="background-image: url('/waterlilies.jpg')">
        <div>
        <h1 class="uk-text-center uk-animation-fade uk-overlay-primary">Home museum (β)<br><small>画面いっぱいに美術作品を楽しもう</small></h1>
        <br>
        <span class="uk-overlay-primary">数十万点以上の美術作品を、画面いっぱいにスライドショーで楽しめるサービスです。</span>
        </div>
      </div>
      <div class="uk-padding uk-animation-fade">
        <div>
          <h2>バーチャルコレクション展</h2>
        </div>
        <div class="uk-child-width-1-2@s uk-child-width-1-3@l" uk-grid v-if="collections.length != 0">
          <div v-for="item in collections" :key="item['id']">
            <a :href="'/prototype/random/collection/' + item['id']">
            <div class="uk-card uk-card-default uk-card-large">
              <div class="uk-card-media-top uk-height-medium uk-flex uk-flex-middle uk-flex-center">
                <img :src="item['image_url']" class="uk-responsive-height uk-responsive-width">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">{{ item['title'] }}</h3>
                <p>作品数: {{ item['count'] }}</p>
              </div>
            </div>
            </a>
          </div>
        </div>
        <div v-else>
          コレクションを読み込んでいます...
        </div>
      </div>
    </div>
  </div>
</template>

<script>
export default {
  name: "Home",
  data: () => { return {
  server: "",
    apiPath: "/v1/collection",
    styles: [
      ["/underthewaveoff-kanagawa.jpg", "日本画", "/prototype/random/style/japan"],
      ["/stacksofwheat-endofsummer.jpg", "印象派", "/prototype/random/style/impress"],
      ["/vasewithloophandles.jpg", "シャッフル （陶芸等含む）", "/prototype/random"],
    ],
    collections: [],
  }},
  mounted() {
    fetch(this.server + this.apiPath)
      .then(function(response) {
        return response.json();
      }).then(json => {
        this.collections = json
      })
  }
};
</script>

<style lang="scss" scoped>

</style>
