import Vue from "vue";
import VueRouter, { RouteConfig } from "vue-router";
import Home from "../views/Home.vue";
import Random from "../views/Random.vue";

Vue.use(VueRouter);

const routes: Array<RouteConfig> = [
  {
    path: "/prototype/",
    name: "Home",
    component: Home
  },
  {
    path: "/prototype/random",
    name: "Random",
    component: Random
  },
  {
    path: "/prototype/random/:type/:name",
    name: "Random (type specified)",
    component: Random
  }
];

const router = new VueRouter({
  mode: "history",
  base: process.env.BASE_URL,
  routes
});

export default router;
