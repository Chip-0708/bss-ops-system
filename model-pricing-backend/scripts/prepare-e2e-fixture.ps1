param([string]$Output = '../../model-pricing-web/public/e2e-fixture.json')
$ErrorActionPreference = 'Stop'
if (-not $env:E2E_TEST_DATABASE_URL) { throw '请设置 E2E_TEST_DATABASE_URL 为测试库连接串。' }
if (-not (Get-Command psql -ErrorAction SilentlyContinue)) { throw '需要 psql 命令。' }

# 只变更明确的测试账号授权和专用客户。已有成本、SKU、策略必须满足真实生成条件。
$sql = @'
BEGIN;
DO $$
DECLARE v_staff bigint; v_role bigint; v_owner bigint; v_subject bigint; v_sku bigint; v_policy bigint; v_margin numeric;
BEGIN
  SELECT owner_id INTO v_staff FROM account WHERE portal_type='INTERNAL' AND login_id='smoke_admin' AND owner_type='STAFF' AND status='ACTIVE';
  SELECT id INTO v_role FROM role WHERE code='PRICING_OP';
  IF v_staff IS NULL OR v_role IS NULL THEN RAISE EXCEPTION '缺少有效 smoke_admin 或 PRICING_OP'; END IF;
  INSERT INTO role_grant(staff_id,role_id,granted_by,granted_at)
    SELECT v_staff,v_role,v_staff,now() WHERE NOT EXISTS
      (SELECT 1 FROM role_grant WHERE staff_id=v_staff AND role_id=v_role AND (expires_at IS NULL OR expires_at>now()));

  SELECT owner_id INTO v_staff FROM account WHERE portal_type='INTERNAL' AND login_id='buyer_b' AND owner_type='STAFF' AND status='ACTIVE';
  SELECT id INTO v_role FROM role WHERE code='FINANCE';
  IF v_staff IS NULL OR v_role IS NULL THEN RAISE EXCEPTION '缺少有效 buyer_b 或 FINANCE'; END IF;
  INSERT INTO role_grant(staff_id,role_id,granted_by,granted_at)
    SELECT v_staff,v_role,v_staff,now() WHERE NOT EXISTS
      (SELECT 1 FROM role_grant WHERE staff_id=v_staff AND role_id=v_role AND (expires_at IS NULL OR expires_at>now()));

  SELECT rg.staff_id INTO v_owner FROM role_grant rg JOIN role r ON r.id=rg.role_id
    JOIN internal_staff s ON s.id=rg.staff_id WHERE r.code='SALES' AND s.status='ACTIVE'
    AND (rg.expires_at IS NULL OR rg.expires_at>now()) ORDER BY rg.staff_id LIMIT 1;
  IF v_owner IS NULL THEN RAISE EXCEPTION '缺少可用 SALES 员工，无法创建测试客户'; END IF;
  SELECT id INTO v_subject FROM legal_subject WHERE subject_type='COMPANY' AND uscc='E2EPRICEBOOKQUOTE1';
  IF v_subject IS NULL THEN
    INSERT INTO legal_subject(subject_type,legal_name,uscc,verification_status)
      VALUES('COMPANY','PRICE_BOOK_QUOTE E2E 测试客户','E2EPRICEBOOKQUOTE1','VERIFIED') RETURNING id INTO v_subject;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM customer_profile WHERE subject_id=v_subject) THEN
    INSERT INTO customer_profile(subject_id,level_code,owner_sales_operator_id,status)
      VALUES(v_subject,'GOLD',v_owner,'ACTIVE');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM customer_profile WHERE subject_id=v_subject AND level_code='GOLD' AND status='ACTIVE') THEN
    RAISE EXCEPTION '专用测试客户状态不是 ACTIVE/GOLD，请人工核对';
  END IF;
  INSERT INTO sys_config(config_key,config_value,description)
    VALUES('min_gross_margin','0.15','最低销售毛利率红线') ON CONFLICT(config_key) DO NOTHING;
  SELECT config_value::numeric INTO v_margin FROM sys_config WHERE config_key='min_gross_margin';
  IF v_margin < 0 OR v_margin >= 0.95 THEN RAISE EXCEPTION 'min_gross_margin 无效或不适合测试策略'; END IF;
  SELECT id,scope_id INTO v_policy,v_sku FROM pricing_policy WHERE code='E2E_PRICE_BOOK_QUOTE_GOLD' LIMIT 1;
  IF v_policy IS NULL THEN
    SELECT s.id INTO v_sku FROM model_sku s JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
      JOIN cost_component cc ON cc.cost_baseline_id=cb.id AND cc.unit_cost>0
      WHERE s.lifecycle_status='PUBLISHED' AND s.native_currency=cb.currency ORDER BY s.id LIMIT 1;
    IF v_sku IS NULL THEN RAISE EXCEPTION '缺少已发布 SKU 的真实当前成本基线和组件'; END IF;
    INSERT INTO pricing_policy(code,name,scope_type,scope_id,level_code,price_method,param_value,priority,status)
      VALUES('E2E_PRICE_BOOK_QUOTE_GOLD','PRICE_BOOK_QUOTE E2E','SKU',v_sku,'GOLD','MARGIN',GREATEST(v_margin+0.05,0.25),10000,'ACTIVE');
  ELSIF NOT EXISTS (SELECT 1 FROM pricing_policy WHERE id=v_policy AND scope_type='SKU' AND level_code='GOLD'
      AND price_method='MARGIN' AND status='ACTIVE' AND param_value>=v_margin) THEN
    RAISE EXCEPTION 'E2E 专用策略状态或毛利参数不满足要求，请人工核对';
  END IF;
END $$;

-- 使用现有真实成本；不可凭空制造供应商成本。缺前置数据则回滚整个准备过程。
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM sys_config WHERE config_key='min_gross_margin' AND config_value::numeric>=0 AND config_value::numeric<1) THEN RAISE EXCEPTION 'min_gross_margin 无效'; END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pricing_policy p JOIN model_sku s ON s.id=p.scope_id
    JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
    JOIN cost_component cc ON cc.cost_baseline_id=cb.id AND cc.unit_cost>0
    WHERE p.code='E2E_PRICE_BOOK_QUOTE_GOLD' AND p.status='ACTIVE'
      AND s.lifecycle_status='PUBLISHED' AND s.native_currency=cb.currency
  ) THEN RAISE EXCEPTION 'E2E 专用 SKU 缺少有效当前成本或已下架'; END IF;
END $$;
COMMIT;
SELECT json_build_object('customer_id', c.id::text, 'sku_ids', json_build_array(s.id::text),
  'policy_id', p.id::text, 'currency', trim(s.native_currency))::text
FROM customer_profile c JOIN legal_subject ls ON ls.id=c.subject_id
CROSS JOIN pricing_policy p JOIN model_sku s ON s.id=p.scope_id
WHERE ls.subject_type='COMPANY' AND ls.uscc='E2EPRICEBOOKQUOTE1' AND p.code='E2E_PRICE_BOOK_QUOTE_GOLD';
'@
$result = $sql | & psql -X -q -A -t -v ON_ERROR_STOP=1 $env:E2E_TEST_DATABASE_URL
if ($LASTEXITCODE -ne 0) { throw 'fixture 准备失败；事务已回滚。' }
$json = ($result | Where-Object { $_ -match '^\{' } | Select-Object -Last 1)
if (-not $json) { throw '未得到 fixture 配置。' }
$null = $json | ConvertFrom-Json
$target = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot $Output))
$directory = Split-Path $target
New-Item -ItemType Directory -Path $directory -Force | Out-Null
[IO.File]::WriteAllText($target, $json + [Environment]::NewLine)
Write-Output "fixture 已检查；非敏感配置：$target"
