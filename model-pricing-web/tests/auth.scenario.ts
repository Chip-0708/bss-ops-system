import test from "node:test";
import assert from "node:assert/strict";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { createPinia, setActivePinia } from "pinia";
import { createRouter, createMemoryHistory } from "vue-router";
import { nextTick } from "vue";
import { login, logout, type LoginResult } from "../src/api/auth";
import { apiRequest } from "../src/api/http";
import { useSessionStore } from "../src/stores/session";
import { usePermissionStore } from "../src/stores/permission";
import { installRouterGuards } from "../src/router/guards";

const entries = new Map<string, string>();
Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: {
  getItem: (key: string) => entries.get(key) ?? null,
  setItem: (key: string, value: string) => entries.set(key, value),
  removeItem: (key: string) => entries.delete(key),
} });
const synthetic: LoginResult = { token: "synthetic-auth-test-only", snapshot: {
  account_id: 41, operator_id: 7, portal_type: "INTERNAL", roles: [{ code: "MODEL_OPS" }], perms: ["M1:V"],
} };

test("real-auth mode logs in all portals, injects only server permissions, keeps credentials in memory and accepts logout 204", async () => {
  const pinia = createPinia(); setActivePinia(pinia);
  const session = useSessionStore();
  const server = setupServer(
    http.post("http://localhost/api/:portal/auth/login", async ({ request, params }) => {
      assert.equal(request.headers.get("Authorization"), null);
      assert.equal(request.headers.get("X-Mock-Identity"), null);
      assert.deepEqual(await request.json(), { login_id: "test-user", password: "synthetic-password" });
      return HttpResponse.json({ code: 0, data: { ...synthetic, snapshot: { ...synthetic.snapshot, portal_type: String(params.portal).toUpperCase() } } });
    }),
    http.post("http://localhost/api/:portal/auth/logout", ({ request }) => {
      assert.equal(request.headers.get("Authorization"), `Bearer ${synthetic.token}`);
      return new HttpResponse(null, { status: 204 });
    }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    session.restoreSession(); assert.equal(session.isAuthenticated, false);
    assert.equal(session.startDevSession("MODEL_OPS"), false);
    for (const portal of ["internal", "supplier", "customer"] as const) {
      session.startSession(await login(portal, "test-user", "synthetic-password"), portal, "test-user");
      assert.equal(session.portal, portal);
      assert.equal(session.isDevSession, false);
      assert.equal(usePermissionStore().canAction("M1:V"), true);
      assert.equal(usePermissionStore().canAction("M1:E"), false);
      assert.equal(entries.size, 0);
      await logout(portal); session.clear();
      assert.equal(session.token, null);
      assert.equal(usePermissionStore().canAction("M1:V"), false);
    }
    setActivePinia(createPinia());
    useSessionStore().restoreSession();
    assert.equal(useSessionStore().isAuthenticated, false);
  } finally { server.close(); session.clear(); }
});

test("login rejection preserves current session, protected 401 redirects, and portals remain isolated", async () => {
  const pinia = createPinia(); setActivePinia(pinia);
  const session = useSessionStore(); session.startSession(synthetic, "internal", "test-user");
  const component = { template: "<div />" };
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/internal/models", component, meta: { requiresAuth: true, portal: "internal" } },
    { path: "/supplier/home", component, meta: { requiresAuth: true, portal: "supplier" } },
    { path: "/internal/login", component, meta: { portal: "internal" } },
    { path: "/supplier/login", component, meta: { portal: "supplier" } },
    { path: "/403", component },
  ] });
  installRouterGuards(router, pinia);
  const server = setupServer(http.all("http://localhost/api/*", () => HttpResponse.json({ code: 10002, message: "unauthorized" }, { status: 401 })));
  server.listen({ onUnhandledRequest: "error" });
  try {
    await assert.rejects(login("internal", "test-user", "wrong"));
    assert.equal(session.token, synthetic.token);
    await router.push("/supplier/login"); assert.equal(router.currentRoute.value.path, "/supplier/login");
    await router.push("/supplier/home"); assert.equal(router.currentRoute.value.path, "/403");
    await router.push("/internal/models");
    await assert.rejects(apiRequest({ url: "/internal/models" }));
    await nextTick(); await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(session.token, null);
    assert.equal(router.currentRoute.value.path, "/internal/login");
    assert.equal(router.currentRoute.value.query.redirect, "/internal/models");
    assert.throws(() => session.startSession({ ...synthetic, snapshot: { ...synthetic.snapshot, account_id: 9007199254740992 } }, "internal", "test-user"));
    assert.throws(() => session.startSession(synthetic, "supplier", "test-user"));
    assert.equal(session.isAuthenticated, false);
  } finally { server.close(); session.clear(); }
});
