import Vue from "vue";
import VueI18n from "vue-i18n";
import App from "./App.vue";
import router from "./router";

Vue.config.productionTip = false;
Vue.use(VueI18n);

const messages = {
  en: {
    home: {
      pageTitle: "Uchibi (Home museum) - STAY HOME. STAY LIVES.",
      title: "Uchibi (Home museum)",
      subtitle: "STAY HOME. SAVE LIVES.",
      description: "Uchibi is a slide show service where you can enjoy more than 400,000 artworks.",
      collection: "The collection",
      number: "Number of artworks: ",
      loading: "Loading collection...",
      claim: "We use public domain works that are freely available.",
      developer: "typewriter",
      nyamikan: "nyamikan.net"
    },
    random: {
      pageTitle: "Uchibi (Home museum): Collection view",
      dependNetwork: "(It may take a long time depending on the network condition)",
      switchInterval: "Artworks change every 1 minute.",
      enterFullscreen: "Press Enter to go fullscreen.",
      retreiveArtworks: "Receive artwork information...",
      retreiveArtworksImage: "Receive artwork image...",
    }
  },
  ja: {
    home: {
      pageTitle: "Uchibi - おうちで画面いっぱいに美術作品を楽しもう",
      title: "Uchibi",
      subtitle: "おうちで画面いっぱいに美術作品を楽しもう",
      description: "40万点以上の美術作品を、画面いっぱいにスライドショーで楽しめるサービスです。",
      collection: "バーチャルコレクション展",
      number: "作品数: ",
      loading: "コレクションを読み込んでいます...",
      claim: "本サービスは、自由に利用できるパブリックドメインの作品を用いています。",
      developer: "たいぷらいたー",
      nyamikan: "にゃみかん(nyamikan.net)"
    },
    random: {
      pageTitle: "Uchibi: コレクション表示",
      dependNetwork: "（ネットワーク回線の状態によっては時間が掛かる場合があります）",
      switchInterval: "作品は1分ごとに切り替わります",
      enterFullscreen: "Enterキーを押すとフルスクリーンになります",
      retreiveArtworks: "作品情報を取得しています...",
      retreiveArtworksImage: "作品画像を取得しています...",
    }
  }
}

const i18n = new VueI18n({
  locale: 'ja',
  messages
})

new Vue({
  router,
  i18n,
  render: h => h(App)
}).$mount("#app");
