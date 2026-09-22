-- 000007_seed.down：回滚种子数据（只删除，不 DROP 任何 DDL）。

-- 授权关系与角色/权限种子
DELETE FROM role_permission;
DELETE FROM role_mutex;
DELETE FROM role;
DELETE FROM permission_point;
-- 系统参数直接清空（种子数据是唯一来源）。
DELETE FROM sys_config;
