<template>
  <div id="app"> <!--
    <div id="nav">
      <router-link to="/about">人気のコレクション</router-link> |
      <router-link to="/random">ランダム</router-link>
    </div> -->
    <router-view />
  </div>
</template>

<script>
import UIkit from 'uikit';
import Icons from 'uikit/dist/js/uikit-icons';

UIkit.use(Icons);

export default {
  name: "App",
}
</script>

<style lang="scss">
// @import "./styles/uikit-customs.scss";


@import "@/styles/variables-hm.scss";
@import "../node_modules/uikit/src/scss/mixins-theme.scss";

@import "../node_modules/uikit/src/scss/uikit-theme.scss";

#app {
  font-family: Avenir, Helvetica, Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  text-align: center;
  color: #2c3e50;
}

#nav {
  padding: 30px;
}
</style>
