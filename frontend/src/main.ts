import "@vue-flow/core/dist/style.css";
import "@fortawesome/fontawesome-free/css/all.min.css";
import "element-plus/dist/index.css";
import "vxe-table/lib/style.css";
import "./styles/app.css";

import ElementPlus from "element-plus";
import { createPinia } from "pinia";
import { createApp } from "vue";
import VxeUITable from "vxe-table";

import App from "./App.vue";
import { router } from "./router";

const app = createApp(App);
const pinia = createPinia();

app.use(pinia);
app.use(router);
app.use(ElementPlus);
app.use(VxeUITable);

app.mount("#app");
