<template>
  <div style="width: 100vw; height: 100vh;" class="uk-light uk-flex uk-flex-center uk-flex-middle uk-text-small">
    <div id="loading" v-if="loading">
      <div uk-spinner="ratio: 3"></div>
      <br>読み込んでいます...
    </div>
    <div v-if="!loading">
      <transition name="fade" mode="out-in" appear>
        <div id="even" key="even" v-if="even" style="width: 100vw; height: 100vh;" class="uk-background-contain" v-bind:style="{ backgroundImage: evenImage }"><!-- :style="{ backgroundImage: 'url(' + require('@/assets/imgs/monet/stacksofwheat-endofsummer.jpg') + ')' }"> -->
          <transition name="fade">
            <div v-if="description" class="uk-overlay uk-padding-small uk-overlay-primary uk-position-bottom">OVERLAY MESSAGE</div>
          </transition>
        </div>
        <div id="odd" key="odd" v-else style="width: 100vw; height: 100vh;" class="uk-background-contain" v-bind:style="{ backgroundImage: oddImage }"><!-- " :style="{ backgroundImage: 'url(' + require('@/assets/imgs/monet/waterlilies.jpg') + ')' }"> -->
          <transition name="fade">
            <div v-if="description" class="uk-overlay uk-padding-small uk-overlay-primary uk-position-bottom">OVERLAY MESSAGE</div>
          </transition>
        </div>
      </transition>
    </div>
  </div>
</template>

<script lang="ts">
import Vue from 'vue'
export default Vue.extend({
  name: 'Random',
  data: function() { return {
    loading: true,     // 初期ロード
    even: true,        // どちらを表示しているか
    description: true, // 説明表示
    evenImage: "",     // イメージアドレス
    oddImage: ""       // イメージアドレス
    // ロード中、切替時間などを可変とする予定である
  }},
  methods: {
    switch() {
      this.description = true
      this.even = !this.even
      this.loaded()
    },
    hideDescription() {
      this.description = false
    },
    loaded() {
      setTimeout(this.switch, 10000)
      // setTimeout(this.hideDescription, 5000)
    }
  },
  mounted() {
    this.evenImage = 'url("/stacksofwheat-endofsummer.jpg")'
    this.oddImage = 'url("/waterlilies.jpg")'
    this.loading = false
    this.loaded()
  }
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