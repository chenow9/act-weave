import "element-plus/es/components/loading/style/css";
import "element-plus/es/components/select/style/css";
import "element-plus/es/components/option/style/css";
import "@fortawesome/fontawesome-free/css/fontawesome.min.css";
import "@fortawesome/fontawesome-free/css/solid.min.css";
import "@fortawesome/fontawesome-free/css/regular.min.css";
import "./styles/app.css";
import "./styles/page-misc.css";
import "./styles/a2ui.css";

import { ElLoading } from "element-plus";
import { createPinia } from "pinia";
import { createApp } from "vue";

import App from "./App.vue";
import { i18n } from "./i18n";
import { router } from "./router";
import { applyDocumentLocale, currentLocale } from "./services/locale";

const app = createApp(App);
const pinia = createPinia();

app.use(pinia);
app.use(router);
app.use(i18n);
app.use(ElLoading);

applyDocumentLocale(currentLocale());

app.mount("#app");
