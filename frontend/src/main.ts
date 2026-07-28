import "element-plus/es/components/loading/style/css";
import "element-plus/es/components/select/style/css";
import "element-plus/es/components/option/style/css";
import "@fortawesome/fontawesome-free/css/fontawesome.min.css";
import "@fortawesome/fontawesome-free/css/solid.min.css";
import "@fortawesome/fontawesome-free/css/regular.min.css";
import "./styles/app.css";
import "./styles/page-misc.css";

import { ElLoading } from "element-plus";
import { createPinia } from "pinia";
import { createApp } from "vue";

import App from "./App.vue";
import { router } from "./router";

const app = createApp(App);
const pinia = createPinia();

app.use(pinia);
app.use(router);
app.use(ElLoading);

app.mount("#app");
