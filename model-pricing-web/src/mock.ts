import { ApiError, statuses } from "./types.ts";
import { createOperations } from './operations.ts';
import { modelSku, modelWriteFields } from "./mocks/data/modelCatalog.ts";
import type { Draft } from "./api/models.types.ts";
import type {
  Model,
  Impact,
  Retirement,
  BatchResult,
  Request,
  Status,
} from "./types.ts";

const copy = <T>(value: T): T => structuredClone(value);
function seeds(): Model[] {
  return Object.keys(statuses).map((status, i) => ({
    id: `900719925474100${i}`,
    code: [
      "gpt-4.1",
      "gpt-4.1-mini",
      "claude-sonnet-4",
      "claude-haiku-4",
      "deepseek-v3",
      "deepseek-r1",
    ][i]!,
    name: [
      "GPT 4.1",
      "GPT 4.1 mini",
      "Claude Sonnet 4",
      "Claude Haiku 4",
      "DeepSeek V3",
      "DeepSeek R1",
    ][i]!,
    vendor: [
      "OpenAI",
      "OpenAI",
      "Anthropic",
      "Anthropic",
      "DeepSeek",
      "DeepSeek",
    ][i]!,
    family: [
      "GPT 4.1",
      "GPT 4.1",
      "Claude 4",
      "Claude 4",
      "DeepSeek",
      "DeepSeek",
    ][i]!,
    type: i === 5 ? "推理" : "对话",
    context: i < 2 ? 1048576 : 128000,
    capabilities: i < 4 ? ["文本", "视觉"] : ["文本"],
    protocol: "OpenAI 兼容",
    tier: i % 2 ? "经济" : "主力",
    sensitive: false,
    crossBorder: i < 4,
    currency: i < 4 ? "USD" : "CNY",
    input: [
      "2.00000000",
      "0.40000000",
      "3.00000000",
      "0.80000000",
      "2.00000000",
      "4.00000000",
    ][i]!,
    output: "8.00000000",
    status: status as Status,
    aliases: [],
    verification: [],
    cost: "1.20000000",
    margin: "24.50",
  }));
}

// All business rules here are an isolated demo contract, never production authorization.
export function createMock() {
  let models = seeds();
  let reports = new Map<string, Impact>();
  let retirements: Retirement[] = [];
  const retirementReports = new Map<string, Impact>();
  const operations = createOperations(() => models, () => retirements, id => retirementReports.get(id));
  let keys = new Map<string, { signature: string; value: unknown }>();
  let nextFailure = false;
  let sequence = 100;
  const id = () => String(9007199254742000n + BigInt(++sequence));
  const find = (modelId: string) => {
    const m = models.find((m) => m.id === modelId);
    if (!m) throw new ApiError(404, "模型不存在，请刷新列表");
    return m;
  };
  const editable = (m: Model) => {
    if (m.status === "OFFLINE" || m.status === "DEPRECATING")
      throw new ApiError(409, "即将下线或已下线模型不可维护");
  };
  const unique = (code: string, except?: string) => {
    if (
      models.some(
        (m) =>
          m.id !== except &&
          (m.code.toLowerCase() === code.toLowerCase() ||
            m.aliases.some((a) => a.toLowerCase() === code.toLowerCase())),
      )
    )
      throw new ApiError(409, "标识或别名已被其他模型使用");
  };
  const validate = (d: Draft) => {
    if (
      !d ||
      !["code", "name", "vendor", "family", "type", "protocol", "tier"].every(
        (k) =>
          typeof d[k as keyof Draft] === "string" &&
          String(d[k as keyof Draft]).trim(),
      )
    )
      throw new ApiError(
        422,
        "请完整填写标识、名称、厂商、系列、类型、协议和分级",
      );
    if (!/^[a-zA-Z0-9._/-]+$/.test(d.code))
      throw new ApiError(
        422,
        "SKU 标识仅支持英文、数字、点、下划线、斜线和连字符",
      );
    if (
      !Number.isInteger(d.context) ||
      (d.context === null || d.context <= 0) ||
      !d.capabilities.length
    )
      throw new ApiError(422, "上下文需为正整数，且至少选择一种能力");
  };
  const draftFields = (d: Draft): Draft => ({
    code: d.code.trim(),
    name: d.name.trim(),
    vendor: d.vendor.trim(),
    family: d.family.trim(),
    type: d.type,
    context: d.context,
    capabilities: [...d.capabilities],
    protocol: d.protocol,
    tier: d.tier,
    sensitive: d.sensitive,
    crossBorder: d.crossBorder,
  });
  function handle(req: Request): unknown {
    if (nextFailure) {
      nextFailure = false;
      throw new ApiError(503, "模拟服务暂不可用，请重试（原数据未变更）");
    }
    if (!req.path.startsWith('/api/internal/models')) return operations.handle(req);
    const b = req.body as any;
    const path = req.path.replace("/api/internal/models", "");
    const write = req.method !== "GET";
    if (
      !write &&
      path !== "" &&
      path !== "/retirements" &&
      !path.endsWith("/suggestions") &&
      !path.endsWith("/deprecation-impact")
    )
      throw new ApiError(405, "此接口不支持读取，不能用 GET 执行写操作");
    const signature = JSON.stringify([req.method, req.path, req.body]);
    const cacheKey = `${req.role}:${req.key}`;
    if (write) {
      if (req.role === "VIEWER")
        throw new ApiError(403, "当前身份没有操作权限");
      if (!req.key) throw new ApiError(400, "缺少 Idempotency-Key");
      const cached = keys.get(cacheKey);
      if (cached) {
        if (cached.signature !== signature)
          throw new ApiError(409, "幂等键已用于其他操作");
        return copy(cached.value);
      }
      if (req.role !== "MODEL_OPS" && !path.endsWith("/publish"))
        throw new ApiError(403, "该操作仅限模型运营");
    }
    let result: unknown;
    if (path === "" && req.method === "GET") {
      result = models.map((m) => {
        const row = copy(m);
        if (req.role !== "PRICING_OP") {
          delete row.cost;
          delete row.margin;
        }
        return row;
      });
    } else if (path === "/retirements" && req.method === "GET") {
      result = retirements;
    } else if (path === "" && req.method === "POST") {
      if (b && Object.hasOwn(b, "sku_code")) {
        const fields = modelWriteFields(b, models);
        const m: Model = { ...fields, id: id(), status: "DRAFT", input: "0.00000000", output: "0.00000000", verification: [] };
        // Legacy-only price placeholders are never projected into the SKU contract response.
        models.push(m);
        result = modelSku(m);
      } else {
      validate(b);
      unique(b.code.trim());
      const m: Model = {
        ...draftFields(b),
        id: id(),
        currency: "CNY",
        input: "0.00000000",
        output: "0.00000000",
        status: "DRAFT",
        aliases: [],
        verification: [],
      };
      models.push(m);
      result = { id: m.id };
      }
    } else if (path === "" && req.method === "PUT") {
      const m = find(b.id);
      editable(m);
      if (Object.hasOwn(b, "sku_code")) {
        const { id: _id, ...value } = b;
        const fields = modelWriteFields(value, models, m);
        Object.assign(m, fields);
        result = modelSku(m);
      } else {
      validate(b);
      unique(b.code.trim(), m.id);
      Object.assign(m, draftFields(b));
      result = { id: m.id };
      }
    } else if (path === "/batch") {
      const contractBatch = Array.isArray(b.sku_ids);
      const ids = contractBatch ? b.sku_ids : b.ids;
      const batchAction = contractBatch
        ? b.action === "SET_TIER" ? "tier" : b.action === "SUBMIT_VERIFY" ? "status" : "unsupported"
        : b.action;
      const batchValue = contractBatch
        ? b.action === "SET_TIER" ? b.payload?.tier_tag : "PENDING_VERIFY"
        : b.value;
      if (contractBatch && (!ids.length || ids.length > 200 || new Set(ids.map(String)).size !== ids.length))
        throw new ApiError(400, "每批必须选择 1~200 个不重复的模型");
      if (contractBatch && ids.some((id: unknown) => typeof id === "number"
        ? !Number.isSafeInteger(id) || id <= 0
        : typeof id !== "string" || !/^[1-9]\d*$/.test(id)))
        throw new ApiError(400, "SKU ID 格式不正确或超出安全整数范围");
      if (contractBatch && !["SET_TIER", "SUBMIT_VERIFY"].includes(b.action))
        throw new ApiError(400, "此页面暂不支持该批量动作；上架和退役需独立接口");
      if (contractBatch && b.action === "SET_TIER" && !["旗舰", "主力", "经济", "长尾"].includes(batchValue))
        throw new ApiError(400, "无效分级");
      if (!contractBatch && (!Array.isArray(ids) || !ids.length))
        throw new ApiError(422, "请先选择模型");
      const items: BatchResult[] = ids.map((rawId: string | number): BatchResult => {
        const mid = String(rawId);
        try {
          const m = find(mid);
          editable(m);
          if (batchAction === "tier") {
            if (!["旗舰", "主力", "经济", "长尾"].includes(batchValue))
              throw new ApiError(422, "无效分级");
            m.tier = batchValue;
          } else if (
            batchAction === "status" &&
            batchValue === "PENDING_VERIFY" &&
            m.status === "DRAFT"
          ) {
            m.status = "PENDING_VERIFY";
          } else {
            throw new ApiError(
              409,
              "仅演示草稿提交待验证；上架、退役需走独立流程",
            );
          }
          return { id: mid, code: m.code, ok: true, reason: "已更新" };
        } catch (e) {
          return {
            id: mid,
            code: models.find((m) => m.id === mid)?.code || mid,
            ok: false,
            reason: (e as Error).message,
          };
        }
      });
      result = contractBatch ? {
        total: items.length,
        succeeded: items.filter((item) => item.ok).length,
        failed: items.filter((item) => !item.ok).map((item) => ({ sku_id: item.id, code: 10001, message: item.reason })),
      } : items;
    } else if (path === "/aliases/merge") {
      const contract = b && "target_sku_id" in b;
      if (contract && (Object.keys(b).some((key) => !["target_sku_id", "source_sku_id", "alias"].includes(key)) ||
          ![b.target_sku_id, b.source_sku_id].every((value) => typeof value === "string" && value.trim() || typeof value === "number" && Number.isSafeInteger(value))))
        throw new ApiError(400, "请提供安全的来源和目标 SKU ID");
      const target = find(contract ? String(b.target_sku_id) : b.targetId);
      if (contract) {
        const source = find(String(b.source_sku_id));
        if (source.id === target.id) throw new ApiError(400, "来源和目标不能是同一个模型");
        editable(source);
      }
      editable(target);
      if (typeof b.alias !== "string" || !b.alias.trim() || b.alias.trim().length > 128)
        throw new ApiError(400, "别名长度须为 1~128 个字符");
      const alias = String(b.alias || "").trim();
      if (!alias) throw new ApiError(422, "请输入待合并的新名称");
      unique(alias);
      if (
        target.code.toLowerCase() === alias.toLowerCase() ||
        target.aliases.some((a) => a.toLowerCase() === alias.toLowerCase())
      )
        throw new ApiError(409, "该名称已属于目标模型");
      target.aliases.push(alias);
      result = contract ? { target_sku_id: target.id, merged_sku_id: String(b.source_sku_id), alias_id: id(), alias } : { id: target.id };
    } else {
      const [, mid, action] = path.split("/");
      const m = find(mid!);
      if (action === "suggestions" && req.method === "GET") {
        const name = String(b?.name || "")
          .toLowerCase()
          .replace(/[^a-z0-9]/g, "");
        result = models
          .filter((x) => x.id !== m.id)
          .map((x) => ({
            id: x.id,
            code: x.code,
            name: x.name,
            score: [...new Set(name)].filter((c) =>
              x.code.toLowerCase().includes(c),
            ).length,
          }))
          .sort((a, b) => b.score - a.score)
          .slice(0, 3);
      } else if (action === "verify") {
        if (req.method !== "POST" || !b || typeof b !== "object" || Array.isArray(b) ||
            Object.keys(b).some(key => !["result", "note"].includes(key)))
          throw new ApiError(400, "人工验证仅允许 POST 提交结果和说明");
        editable(m);
        if (m.status !== "PENDING_VERIFY")
          throw new ApiError(409, "仅待验证模型可记录人工验证");
        if (
          !["PASS", "FAIL"].includes(b.result) ||
          typeof b.note !== "string" || !b.note.trim() || b.note.trim().length > 1000
        )
          throw new ApiError(422, "请选择结果并填写 1~1000 字的验证说明（Mock 暂定限制）");
        m.verification.push({
          by: "林悦",
          at: new Date().toISOString(),
          result: b.result,
          note: b.note.trim(),
        });
        if (b.result === "PASS") m.status = "PURCHASABLE";
        result = { id: m.id };
      } else if (action === "aliases") {
        editable(m);
        if (Array.isArray(b.aliases)) {
          if (b.source !== undefined && !["MANUAL", "IMPORT"].includes(b.source))
            throw new ApiError(400, "别名来源不合法");
          if (b.aliases.some((value: unknown) => typeof value !== "string" || !value.trim() || value.trim().length > 128))
            throw new ApiError(400, "每个别名必须是 1~128 个字符");
          const aliases: string[] = b.aliases.map((value: string) => value.trim());
          if (new Set(aliases.map((value) => value.toLowerCase())).size !== aliases.length)
            throw new ApiError(400, "别名不能重复");
          for (const alias of aliases) {
            try {
              unique(alias, m.id);
            } catch {
              throw new ApiError(400, "别名已被其他模型使用");
            }
            if (m.code.toLowerCase() === alias.toLowerCase())
              throw new ApiError(400, "别名不能与自身 SKU 编码相同");
          }
          // Validate the entire replacement before mutating; no partial deletion on failure.
          m.aliases = aliases;
          result = { id: m.id };
        } else {
        const alias = String(b.alias || "").trim();
        if (!alias) throw new ApiError(422, "别名不能为空");
        if (b.action === "remove") {
          if (!m.aliases.includes(alias)) throw new ApiError(404, "别名不存在");
          m.aliases = m.aliases.filter((a) => a !== alias);
        } else {
          unique(alias);
          if (
            m.code.toLowerCase() === alias.toLowerCase() ||
            m.aliases.some((a) => a.toLowerCase() === alias.toLowerCase())
          )
            throw new ApiError(409, "别名已存在");
          m.aliases.push(alias);
        }
        result = { id: m.id };
        }
      } else if (action === "publish") {
        if (req.method !== "POST" || !b || typeof b !== "object" || Array.isArray(b) || Object.keys(b).some(key => key !== "remark") || (b.remark !== undefined && typeof b.remark !== "string"))
          throw new ApiError(400, "上架仅允许 POST 提交可选备注，不接受状态或价格字段");
        if (req.role !== "PRICING_OP")
          throw new ApiError(403, "上架仅限定价运营");
        if (m.status !== "PURCHASABLE")
          throw new ApiError(409, "仅可采购模型允许上架");
        m.status = "PUBLISHED";
        result = { id: m.id, sku_id: m.id, lifecycle_status: m.status, published_at: new Date().toISOString() };
      } else if (action === "deprecation-impact" && req.method === "GET") {
        const report: Impact = {
          id: id(),
          modelId: m.id,
          books: ["标准价目表 · 演示"],
          contracts: ["客户合同 DEMO-001"],
          quotes: ["客户报价 DEMO-002"],
          alternatives: models
            .filter(
              (x) =>
                x.id !== m.id &&
                ["PURCHASABLE", "PUBLISHED"].includes(x.status),
            )
            .map((x) => x.code),
          notifiedAt: "2026-09-01T00:00:00+08:00",
        };
        reports.set(report.id, report);
        result = report;
      } else if (action === "deprecate") {
        if (req.method !== "POST" || !b || typeof b !== "object" || Array.isArray(b) ||
            Object.keys(b).some(key => !["impactId", "offlineAt", "reason", "replacementId"].includes(key)))
          throw new ApiError(400, "退役仅允许POST提交报告、日期和理由");
        if (m.status !== "PUBLISHED" || m.pendingRetirement)
          throw new ApiError(409, "仅已上架且无待审批退役单的模型可以发起");
        const report = reports.get(b.impactId);
        if (!report || report.modelId !== m.id)
          throw new ApiError(422, "请先加载有效的影响报告");
        if (typeof b.reason !== "string" || !b.reason.trim() || Array.from(b.reason.trim()).length > 500)
          throw new ApiError(422, "退役理由须为1~500字");
        const date = Date.parse(`${b.offlineAt}T00:00:00+08:00`);
        if (
          !/^\d{4}-\d{2}-\d{2}$/.test(b.offlineAt) ||
          !Number.isFinite(date) ||
          new Date(date + 8 * 3600000).toISOString().slice(0, 10) !== b.offlineAt ||
          !Number.isFinite(Date.parse(report.notifiedAt)) ||
          date < Date.parse(report.notifiedAt) + 30 * 86400000 ||
          date <= Date.now()
        )
          throw new ApiError(
            422,
            "下线日须晚于今天，且距通知任务发出日至少30天（当前通知日期为Mock演示数据）",
          );
        if (b.replacementId !== undefined && (!models.some(model => model.id === String(b.replacementId)) || String(b.replacementId) === m.id))
          throw new ApiError(400, "替代SKU不存在或与退役SKU相同");
        const r: Retirement = {
          id: id(),
          modelId: m.id,
          status: "APPROVING",
          firstApprover: "定价运营",
          replacementId: b.replacementId === undefined ? undefined : String(b.replacementId),
          offlineAt: b.offlineAt,
          reason: b.reason.trim(),
        };
        retirements.push(r);
        retirementReports.set(r.id, copy(report));
        m.pendingRetirement = true;
        result = r;
      } else throw new ApiError(404, "演示接口未实现");
    }
    if (write) keys.set(cacheKey, { signature, value: copy(result) });
    return copy(result);
  }
  return {
    handle,
    markQuotedSkusPurchasable(ids: string[]) {
      for (const model of models) if (ids.includes(model.id) && model.status === "PENDING_VERIFY") model.status = "PURCHASABLE";
    },
    failNext() {
      nextFailure = true;
    },
    reset() {
      models = seeds();
      reports.clear();
      retirements = [];
      retirementReports.clear();
      operations.reset();
      keys.clear();
      nextFailure = false;
    },
  };
}
export const mock = createMock();
