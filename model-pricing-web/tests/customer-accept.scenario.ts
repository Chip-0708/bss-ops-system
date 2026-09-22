import test from "node:test";
import assert from "node:assert/strict";
import { setupServer } from "msw/node";
import { customerPortalHandlers } from "../src/mocks/handlers/customerPortal";

Object.defineProperty(globalThis, "location", { configurable: true, value: new URL("http://localhost/") });
const server = setupServer(...customerPortalHandlers);
const quoteId = "cq-blue-formal-demo";
const headers = { "X-Mock-Identity": "CUSTOMER" };

test("accepted customer quote stays effective after refresh and rejects another submission", async () => {
  server.listen({ onUnhandledRequest: "error" });
  try {
    const list = async () => {
      const response = await fetch("http://localhost/api/customer/quotes?kind=QUOTE&page=1&size=100", { headers });
      assert.equal(response.status, 200);
      return (await response.json()).data.list as Array<{ id: string; status: string; can_accept: boolean }>;
    };
    const accept = (key: string, identity = "CUSTOMER") => fetch(`http://localhost/api/customer/quotes/${quoteId}/accept`, {
      method: "POST", headers: { "X-Mock-Identity": identity, "Idempotency-Key": key, "Content-Type": "application/json" },
      body: "{}",
    });
    assert.equal((await list()).find(item => item.id === quoteId)?.can_accept, true);
    assert.equal((await accept("unauthorized", "VIEWER")).status, 403);
    assert.equal((await list()).find(item => item.id === quoteId)?.status, "FORMAL");
    const accepted = await accept("accept-once");
    assert.equal(accepted.status, 200);
    assert.equal((await accepted.json()).data.new_status, "EFFECTIVE");
    const refreshed = (await list()).find(item => item.id === quoteId);
    assert.equal(refreshed?.status, "EFFECTIVE");
    assert.equal(refreshed?.can_accept, false);
    assert.equal((await accept("accept-again")).status, 409);
    assert.equal((await list()).find(item => item.id === quoteId)?.status, "EFFECTIVE");
  } finally { server.close(); }
});
