-- 000007_seed：全部初始化数据集中在这一文件。仅在此文件里允许 ON CONFLICT DO NOTHING 幂等保护。
-- 矩阵按设计文档 3.4 V/E/A/C/P（M1–M12 共 60 权限点）；§3.4 修正特别注明 M5 多角色共享编辑权。

-- 1. 权限点（60 行）
INSERT INTO permission_point(module_code, action_code, name, created_by, updated_by, request_id, created_at, updated_at) VALUES
 ('M1','V','模型管理·查看',NULL,NULL,NULL,now(),now()),('M1','E','模型管理·编辑',NULL,NULL,NULL,now(),now()),('M1','A','模型管理·审批',NULL,NULL,NULL,now(),now()),('M1','C','模型管理·配置',NULL,NULL,NULL,now(),now()),('M1','P','模型管理·特权',NULL,NULL,NULL,now(),now()),
 ('M2','V','官方价格同步·查看',NULL,NULL,NULL,now(),now()),('M2','E','官方价格同步·编辑',NULL,NULL,NULL,now(),now()),('M2','A','官方价格同步·审批',NULL,NULL,NULL,now(),now()),('M2','C','官方价格同步·配置',NULL,NULL,NULL,now(),now()),('M2','P','官方价格同步·特权',NULL,NULL,NULL,now(),now()),
 ('M3','V','供应商管理·查看',NULL,NULL,NULL,now(),now()),('M3','E','供应商管理·编辑',NULL,NULL,NULL,now(),now()),('M3','A','供应商管理·审批',NULL,NULL,NULL,now(),now()),('M3','C','供应商管理·配置',NULL,NULL,NULL,now(),now()),('M3','P','供应商管理·特权',NULL,NULL,NULL,now(),now()),
 ('M4','V','报价管理·查看',NULL,NULL,NULL,now(),now()),('M4','E','报价管理·编辑',NULL,NULL,NULL,now(),now()),('M4','A','报价管理·审批',NULL,NULL,NULL,now(),now()),('M4','C','报价管理·配置',NULL,NULL,NULL,now(),now()),('M4','P','报价管理·特权',NULL,NULL,NULL,now(),now()),
 ('M5','V','成本管理·查看',NULL,NULL,NULL,now(),now()),('M5','E','成本管理·编辑',NULL,NULL,NULL,now(),now()),('M5','A','成本管理·审批',NULL,NULL,NULL,now(),now()),('M5','C','成本管理·配置',NULL,NULL,NULL,now(),now()),('M5','P','成本管理·特权',NULL,NULL,NULL,now(),now()),
 ('M6','V','定价策略·查看',NULL,NULL,NULL,now(),now()),('M6','E','定价策略·编辑',NULL,NULL,NULL,now(),now()),('M6','A','定价策略·审批',NULL,NULL,NULL,now(),now()),('M6','C','定价策略·配置',NULL,NULL,NULL,now(),now()),('M6','P','定价策略·特权',NULL,NULL,NULL,now(),now()),
 ('M7','V','价目表·查看',NULL,NULL,NULL,now(),now()),('M7','E','价目表·编辑',NULL,NULL,NULL,now(),now()),('M7','A','价目表·审批',NULL,NULL,NULL,now(),now()),('M7','C','价目表·配置',NULL,NULL,NULL,now(),now()),('M7','P','价目表·特权',NULL,NULL,NULL,now(),now()),
 ('M8','V','客户报价·查看',NULL,NULL,NULL,now(),now()),('M8','E','客户报价·编辑',NULL,NULL,NULL,now(),now()),('M8','A','客户报价·审批',NULL,NULL,NULL,now(),now()),('M8','C','客户报价·配置',NULL,NULL,NULL,now(),now()),('M8','P','客户报价·特权',NULL,NULL,NULL,now(),now()),
 ('M9','V','客户管理·查看',NULL,NULL,NULL,now(),now()),('M9','E','客户管理·编辑',NULL,NULL,NULL,now(),now()),('M9','A','客户管理·审批',NULL,NULL,NULL,now(),now()),('M9','C','客户管理·配置',NULL,NULL,NULL,now(),now()),('M9','P','客户管理·特权',NULL,NULL,NULL,now(),now()),
 ('M10','V','财务结算·查看',NULL,NULL,NULL,now(),now()),('M10','E','财务结算·编辑',NULL,NULL,NULL,now(),now()),('M10','A','财务结算·审批',NULL,NULL,NULL,now(),now()),('M10','C','财务结算·配置',NULL,NULL,NULL,now(),now()),('M10','P','财务结算·特权',NULL,NULL,NULL,now(),now()),
 ('M11','V','组织与权限·查看',NULL,NULL,NULL,now(),now()),('M11','E','组织与权限·编辑',NULL,NULL,NULL,now(),now()),('M11','A','组织与权限·审批',NULL,NULL,NULL,now(),now()),('M11','C','组织与权限·配置',NULL,NULL,NULL,now(),now()),('M11','P','组织与权限·特权',NULL,NULL,NULL,now(),now()),
 ('M12','V','审计·查看',NULL,NULL,NULL,now(),now()),('M12','E','审计·编辑',NULL,NULL,NULL,now(),now()),('M12','A','审计·审批',NULL,NULL,NULL,now(),now()),('M12','C','审计·配置',NULL,NULL,NULL,now(),now()),('M12','P','审计·特权',NULL,NULL,NULL,now(),now());

-- 2. 角色（11 种）；data_scope / field_mask / is_functional 依据设计文档 3.2 规则
INSERT INTO role(code, name, data_scope, field_mask, is_functional, created_at, updated_at, request_id, created_by, updated_by) VALUES
 ('PLATFORM_ADMIN','平台管理员',  'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('MODEL_OPS',     '模型运营',   'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('PROCUREMENT',   '采购',       'SELF',NULL, false, now(), now(), NULL, NULL, NULL),
 ('PRICING_OP',    '定价运营',   'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('SALES',         '销售',       'SELF','{"hide":["cost","margin","baseline"]}', false, now(), now(), NULL, NULL, NULL),
 ('FINANCE',       '财务',       'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('OPS_ADMIN',     '运维管理员', 'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('RETRO_OP',      '补录专员',   'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('AUDIT_READONLY','审计只读',   'ALL', NULL, true,  now(), now(), NULL, NULL, NULL),
 ('SUPPLIER',      '供应商',     'SELF','{"hide":["cost","margin","baseline"]}', false, now(), now(), NULL, NULL, NULL),
 ('CUSTOMER',      '客户',       'SELF','{"hide":["cost","margin","baseline"]}', false, now(), now(), NULL, NULL, NULL);

-- 3. 权限矩阵（§3.4 / §3.2 / §3.4 修正）
-- 辅助函数：给指定角色打入一组权限点的 code
DO $$
BEGIN
  -- PLATFORM_ADMIN：全部 60 条
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON true
  WHERE r.code = 'PLATFORM_ADMIN'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- MODEL_OPS：M1/M2 全权限 + 其余仅查看 + M5 修正（成本参数编辑权限）
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code IN ('M1','M2','M5') AND p.action_code!='A') OR
    (p.module_code IN ('M3','M4','M6','M7','M8','M9','M10','M11','M12') AND p.action_code='V')
  )
  WHERE r.code = 'MODEL_OPS'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- PROCUREMENT：M3 全权限 + M4 全权限（报价单提交审批）+ M5 E（主供应商锁定）
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code IN ('M3','M4')) OR
    (p.module_code = 'M5' AND p.action_code = 'E') OR
    (p.module_code IN ('M1','M2','M6','M7','M8','M9','M10','M11','M12') AND p.action_code = 'V')
  )
  WHERE r.code = 'PROCUREMENT'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- PRICING_OP：M5 E（成本参数）+ M6/M7 全 + M8 A（特价审批）+ 仅查看其余
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code = 'M5' AND p.action_code = 'E') OR
    (p.module_code IN ('M6','M7')) OR
    (p.module_code = 'M8' AND p.action_code = 'A') OR
    (p.module_code IN ('M1','M2','M3','M4','M9','M10','M11','M12') AND p.action_code = 'V')
  )
  WHERE r.code = 'PRICING_OP'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- SALES：M8 全 + M9 全（客户归属），成本/毛利/基线被字段剔除
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code IN ('M8','M9')) OR
    (p.module_code IN ('M1','M2','M3','M4','M5','M6','M7','M10','M11','M12') AND p.action_code = 'V')
  )
  WHERE r.code = 'SALES'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- FINANCE：M5 E（汇率锁定）+ M10 全 + 其余仅查看
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code = 'M5' AND p.action_code = 'E') OR
    (p.module_code = 'M10') OR
    (p.module_code IN ('M1','M2','M3','M4','M6','M7','M8','M9','M11','M12') AND p.action_code = 'V')
  )
  WHERE r.code = 'FINANCE'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- OPS_ADMIN：M11 全（组织与权限）+ 其余仅查看
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code = 'M11') OR
    (p.module_code IN ('M1','M2','M3','M4','M5','M6','M7','M8','M9','M10','M12') AND p.action_code = 'V')
  )
  WHERE r.code = 'OPS_ADMIN'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- RETRO_OP：补录特权（P = 补录申请），数据域 ALL；文档未对具体模块组合做显式穷举，扩张为最小实场景
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code IN ('M1','M2','M4','M5','M6','M7','M8') AND p.action_code IN ('E','P','A')) OR
    (p.module_code IN ('M3','M9','M10','M11','M12') AND p.action_code = 'V')
  )
  WHERE r.code = 'RETRO_OP'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- AUDIT_READONLY：M12 全 + 其余仅 V
  INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
  SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
  FROM role r JOIN permission_point p ON (
    (p.module_code = 'M12' AND p.action_code IN ('V','E','A','C','P')) OR
    (p.module_code IN ('M1','M2','M3','M4','M5','M6','M7','M8','M9','M10','M11') AND p.action_code = 'V')
  )
  WHERE r.code = 'AUDIT_READONLY'
  ON CONFLICT (role_id, permission_point_id) DO NOTHING;

  -- SUPPLIER / CUSTOMER：零内部权限（门户边界在网关层，不做 role_permission 条目）
END $$;

-- 4. 角色互斥
INSERT INTO role_mutex(role_a, role_b, created_at, updated_at, request_id, created_by, updated_by) VALUES
 ('SALES','PRICING_OP',now(),now(),NULL,NULL,NULL),
 ('SALES','PROCUREMENT',now(),now(),NULL,NULL,NULL)
ON CONFLICT (role_a, role_b) DO NOTHING;

-- 5. 系统参数（逐字照搬设计文档 2.4 之 INSERT 语句，此处仅换 ON CONFLICT）
INSERT INTO sys_config(config_key, config_value, description) VALUES
 ('retro_monthly_limit', '5', '特权补录当月告警阈值'),
 ('credit_warn_ratio',   '0.80', '授信占用预警阈值'),
 ('credit_freeze_ratio', '1.00', '授信占用冻结阈值'),
 ('settle_warn_ratio',   '0.80', '应付占用预警阈值'),
 ('quote_anomaly_pct',   '0.20', '报价偏离上一版本阈值'),
 ('quote_anomaly_mkt',   '0.30', '报价偏离市场最低价阈值'),
 ('min_gross_margin',    '0.15', '最低销售毛利率红线'),
 ('quote_grace_days',    '3',    '报价到期宽限期天数'),
 ('price_up_notice_days','30',   '涨价提前通知天数')
ON CONFLICT (config_key) DO NOTHING;