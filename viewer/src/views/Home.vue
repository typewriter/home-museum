<template>
  <div>
    <div id="nav">
      人気のコレクション(準備中) |
      <router-link to="/prototype/random/japan">日本画</router-link> |
      <router-link to="/prototype/random/impress">印象派</router-link> |
      西洋画(準備中) |
      陶芸作品(準備中) |
      <router-link to="/prototype/random">ランダム</router-link>
    </div>
    <div class="home uk-light uk-background-secondary">
      <div class="uk-margin uk-height-large uk-flex uk-flex-middle uk-flex-center uk-background-cover uk-background-bottom-center" style="background-image: url('/waterlilies.jpg')">
        <h1 class="uk-text-center uk-animation-fade uk-overlay-primary">Home museum (β)<br><small>画面いっぱいに美術作品を楽しもう</small></h1>
      </div>
      <div class="uk-padding uk-animation-fade">
        <div>
          <h2>画風で選ぶ</h2>
        </div>
        <div class="uk-child-width-1-2@s uk-child-width-1-3@l" uk-grid>
          <div v-for="item in styles" :key="item[0]">
            <a :href="item[2]">
            <div class="uk-card uk-card-default uk-card-large">
              <div class="uk-card-media-top uk-height-medium uk-flex uk-flex-middle uk-flex-center">
                <img :src="item[0]" class="uk-responsive-height uk-responsive-width">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">{{ item[1] }}</h3>
              </div>
            </div>
            </a>
          </div>
        </div>
      </div>
      <div class="uk-padding uk-animation-fade">
        <div>
          コレクションやその他機能は準備中です。
        </div>
      </div>
    </div>
  </div>
</template>

<script>
export default {
  name: "Home",
  data: () => { return {
    styles: [
      ["/underthewaveoff-kanagawa.jpg", "日本画", "/prototype/random/japan"],
      ["/stacksofwheat-endofsummer.jpg", "印象派", "/prototype/random/impress"],
      ["/vasewithloophandles.jpg", "シャッフル （陶芸等含む）", "/prototype/random"],
    ]
  }}
};
</script>

<style lang="scss" scoped>

</style>
