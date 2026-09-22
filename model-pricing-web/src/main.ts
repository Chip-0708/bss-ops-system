import { createApp } from "vue";
import { createPinia } from "pinia";
import ElementPlus from "element-plus";
import zhCn from "element-plus/es/locale/lang/zh-cn";
import "element-plus/dist/index.css";
import "element-plus/theme-chalk/dark/css-vars.css";
import "./styles/index.css";
import App from "./App.vue";
import { createAppRouter } from "./router";

const app = createApp(App);
const pinia = createPinia();
const router = createAppRouter(pinia);

async function startApp() {
  if (import.meta.env.VITE_MOCK_ENABLED === "true") {
    if (!import.meta.env.DEV) {
      throw new Error("Mock 只能在开发构建中启用。");
    }
    const { worker } = await import("./mocks/browser");
    await worker.start({ onUnhandledRequest: "bypass" });
  }
  app.use(pinia).use(router).use(ElementPlus, { locale: zhCn }).mount("#app");
}

void startApp();
