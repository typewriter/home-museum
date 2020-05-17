import Vue from "vue";
import VueRouter, { RouteConfig } from "vue-router";
import Home from "../views/Home.vue";
import Random from "../views/Random.vue";

Vue.use(VueRouter);

const routes: Array<RouteConfig> = [
  {
    path: "/",
    name: "Home",
    component: Home
  },
  {
    path: "/random",
    name: "Random",
    component: Random
  },
  {
    path: "/random/:type/:name",
    name: "Random (type specified)",
    component: Random
  },
  {
    path: "/:lang/",
    name: "Home",
    component: Home
  },
  {
    path: "/:lang/random",
    name: "Random",
    component: Random
  },
  {
    path: "/:lang/random/:type/:name",
    name: "Random (type specified)",
    component: Random
  },
];

const router = new VueRouter({
  mode: "history",
  base: process.env.BASE_URL,
  routes
});

export default router;
