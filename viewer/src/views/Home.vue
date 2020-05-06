<template>
  <div>
    <div id="nav">
      <router-link to="/about">人気のコレクション</router-link> |
      <router-link to="/random">ランダム</router-link>
    </div>
    <div class="home uk-light uk-background-secondary">
      <div class="uk-margin uk-height-large uk-flex uk-flex-middle uk-flex-center uk-background-cover uk-background-bottom-center" style="background-image: url('/waterlilies.jpg')">
        <h1 class="uk-text-center uk-animation-fade uk-overlay-primary">Home museum<br><small>画面いっぱいに美術作品を楽しもう</small></h1>
      </div>
      <div class="uk-padding uk-animation-fade">
        <div>
          <h2>コレクション</h2>
        </div>
        <div class="uk-child-width-1-2@s" uk-grid>
          <div>
            <div class="uk-card uk-card-default">
              <div class="uk-card-media-top">
                <img src="/stacksofwheat-endofsummer.jpg">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">クロード・モネ</h3>
              </div>
            </div>
          </div>
          <div>
            <div class="uk-card uk-card-default">
              <div class="uk-card-media-top">
                <img src="/stacksofwheat-endofsummer.jpg">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">クロード・モネ</h3>
              </div>
            </div>
          </div>
          <div>
            <div class="uk-card uk-card-default">
              <div class="uk-card-media-top">
                <img src="/stacksofwheat-endofsummer.jpg">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">クロード・モネ</h3>
              </div>
            </div>
          </div>
          <div>
            <div class="uk-card uk-card-default">
              <div class="uk-card-media-top">
                <img src="/stacksofwheat-endofsummer.jpg">
              </div>
              <div class="uk-card-body">
                <h3 class="uk-card-title">クロード・モネ</h3>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
export default {
  name: "Home",
};
</script>

<style lang="scss" scoped>

</style>
