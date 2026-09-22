<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";

const route = useRoute();
const title = computed(() => route.meta.title || "页面框架");
const description = computed(
  () => route.meta.description || "该页面正在按需求规格逐步实现。",
);
const isSystemPage = computed(() =>
  ["/403", "/configuration-error"].includes(route.path) ||
  route.matched.some((item) => item.path.includes(":pathMatch")),
);
</script>

<template>
  <section class="placeholder-page">
    <div class="placeholder-page__mark">{{ isSystemPage ? "!" : "◇" }}</div>
    <p class="placeholder-page__eyebrow">
      {{ isSystemPage ? "SYSTEM MESSAGE" : "PAGE FRAMEWORK READY" }}
    </p>
    <h1>{{ title }}</h1>
    <p>{{ description }}</p>
    <el-alert
      v-if="!isSystemPage"
      title="页面路由与门户框架已建立；业务功能、API 和数据保存尚未实现。"
      type="info"
      :closable="false"
      show-icon
    />
    <el-button v-else @click="$router.back()">返回上一页</el-button>
  </section>
</template>

