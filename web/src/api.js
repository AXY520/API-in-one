let adminKey = localStorage.getItem('api-in-one-admin-key') || ''

export function setAdminKey(key) {
  adminKey = key
  localStorage.setItem('api-in-one-admin-key', key)
}

export function clearAdminKey() {
  adminKey = ''
  localStorage.removeItem('api-in-one-admin-key')
}

export function getAdminKey() {
  return adminKey
}

export async function api(path, opts = {}) {
  const resp = await fetch(path, {
    ...opts,
    headers: {
      Authorization: 'Bearer ' + adminKey,
      'Content-Type': 'application/json',
      ...(opts.headers || {}),
    },
  })
  if (!resp.ok) {
    const body = await resp.text().catch(() => '')
    throw new Error(body || `HTTP ${resp.status}`)
  }
  return resp.json()
}

export function channelPath(name) {
  return `/admin/channels/by-name?name=${encodeURIComponent(name)}`
}

export async function fetchChannels() {
  const data = await api('/admin/channels')
  return data.channels || []
}

export async function fetchChannelKeys(name) {
  return api(`/admin/channels/by-name/keys?name=${encodeURIComponent(name)}`)
}

export async function updateChannelKeys(name, body) {
  return api(`/admin/channels/by-name/keys?name=${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function createChannel(body) {
  return api('/admin/channels', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function updateChannel(name, body) {
  return api(channelPath(name), {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function deleteChannel(name) {
  return api(channelPath(name), { method: 'DELETE' })
}

export async function setChannelEnabled(name, enabled) {
  return api(`/admin/channels/by-name/state?name=${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify({ enabled }),
  })
}

export async function updateChannelRouting(name, body) {
  return api(`/admin/channels/by-name/routing?name=${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function updateChannelModelState(name, model, disabled, recover = false) {
  return api(`/admin/channels/by-name/models/state?name=${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify({ model, disabled, recover }),
  })
}

export async function updateChannelKeyState(name, index, disabled, recover = false) {
  return api(`/admin/channels/by-name/keys/${index}/state?name=${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify({ disabled, recover }),
  })
}

export async function probeChannelKeys(name, params = {}) {
  const q = new URLSearchParams()
  if (params.model) q.set('model', params.model)
  if (params.index !== undefined && params.index !== null) q.set('index', params.index)
  if (params.protocol) q.set('protocol', params.protocol)
  const query = q.toString()
  return api(`/admin/channels/by-name/probe?name=${encodeURIComponent(name)}${query ? '&' + query : ''}`, { method: 'POST' })
}

export async function fetchModels(baseURL, key, type = 'openai') {
  return api('/admin/models/fetch', {
    method: 'POST',
    body: JSON.stringify({ type, base_url: baseURL, key }),
  })
}

export async function fetchSettings() {
  return api('/admin/settings')
}

export async function updateAccessKeys(accessKeys) {
  return api('/admin/access-keys', {
    method: 'PUT',
    body: JSON.stringify({ access_keys: accessKeys }),
  })
}

export async function updateModelSystemPrompts(prompts) {
  return api('/admin/model-system-prompts', {
    method: 'PUT',
    body: JSON.stringify({ model_system_prompts: prompts }),
  })
}

export async function updateKeyFailurePolicy(threshold, cooldownSeconds) {
  return api('/admin/key-failure-policy', {
    method: 'PUT',
    body: JSON.stringify({ threshold, cooldown_seconds: cooldownSeconds }),
  })
}

export async function updateChannelModelFailurePolicy(threshold) {
  return api('/admin/channel-model-failure-policy', {
    method: 'PUT',
    body: JSON.stringify({ threshold }),
  })
}

export async function fetchStats() {
  return api('/admin/stats')
}

export async function fetchLogs(params = {}) {
  const q = new URLSearchParams()
  if (params.limit) q.set('limit', params.limit)
  if (params.page) q.set('page', params.page)
  if (params.protocol) q.set('protocol', params.protocol)
  if (params.model) q.set('model', params.model)
  if (params.channel) q.set('channel', params.channel)
  if (params.accessKey) q.set('access_key', params.accessKey)
  if (params.status) q.set('status', params.status)
  if (params.q) q.set('q', params.q)
  return api(`/admin/logs?${q.toString()}`)
}

export async function fetchLog(id) {
  return api(`/admin/logs/${id}`)
}

export async function clearLogs() {
  return api('/admin/logs', { method: 'DELETE' })
}

export async function reloadConfig() {
  return api('/admin/channels/reload', { method: 'POST' })
}
