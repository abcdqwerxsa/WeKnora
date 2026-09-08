import assert from 'node:assert/strict'
import test from 'node:test'

import {
  SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE,
  SETTINGS_SECTION_MIN_ROLE,
  SYSTEM_ADMIN_SETTINGS_SECTIONS,
} from './settingsAccess'

test('management shortcuts are stricter than read-only settings pages', () => {
  assert.equal(SETTINGS_SECTION_MIN_ROLE.members, 'viewer')
  assert.equal(SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE.members, 'owner')
  // LuoSA 客户侧部署场景：vLLM 由租户管理员在 Web 端统一配置一次后，
  // 普通员工全程不接触任何模型设置入口。整张设置页和头像菜单里的
  // 管理快捷入口同级别收紧到 admin，而不是让 viewer 看到只读列表。
  assert.equal(SETTINGS_SECTION_MIN_ROLE.models, 'admin')
  assert.equal(SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE.models, 'admin')
})

test('the skill catalog is admin-only like the sandbox it installs into', () => {
  assert.equal(SETTINGS_SECTION_MIN_ROLE.skills, 'admin')
  assert.equal(SETTINGS_SECTION_MIN_ROLE.skills, SETTINGS_SECTION_MIN_ROLE.sandbox)
  assert.equal(SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE.skills, 'admin')
})

test('personal skill environment variables are visible to every member', () => {
  assert.equal(SETTINGS_SECTION_MIN_ROLE.envvars, 'viewer')
  // Workspace-wide skill env values live on the Admin+ skills page; a
  // management shortcut on the avatar menu would only duplicate that entrance.
  assert.equal(
    Object.prototype.hasOwnProperty.call(SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE, 'envvars'),
    false,
  )
})

test('system administration settings stay explicitly system-admin-only', () => {
  assert.deepEqual(
    [...SYSTEM_ADMIN_SETTINGS_SECTIONS],
    ['system-global', 'runtime-queues', 'platform-api-keys', 'system-audit-log'],
  )
})
