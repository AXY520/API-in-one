export function channelCanServeModel(channel, model) {
  const models = channel.models || []
  const mapping = channel.model_mapping || {}
  if (mapping[model] && models.includes(mapping[model])) return true
  return models.includes(model)
}

export function resolvedModel(channel, model) {
  return (channel.model_mapping || {})[model] || model
}

export function isModelDisabled(channel, model) {
  const disabled = channel.disabled_models || []
  const resolved = resolvedModel(channel, model)
  return disabled.includes(model) || disabled.includes(resolved)
}

export function isChannelEnabled(channel) {
  return channel.enabled !== false
}

export function isModelSchedulable(channel, model) {
  return isChannelEnabled(channel) && channelCanServeModel(channel, model) && !isModelDisabled(channel, model)
}

export function collectModels(channels, { enabledOnly = false } = {}) {
  const source = enabledOnly ? channels.filter(isChannelEnabled) : channels
  const hiddenUpstream = new Set()
  source.forEach(channel => {
    Object.values(channel.model_mapping || {}).forEach(model => hiddenUpstream.add(model))
  })

  const seen = new Set()
  const result = []
  source.forEach(channel => {
    Object.keys(channel.model_mapping || {}).forEach(model => {
      if (!seen.has(model)) {
        seen.add(model)
        result.push(model)
      }
    })
    ;(channel.models || []).forEach(model => {
      if (hiddenUpstream.has(model)) return
      if (!seen.has(model)) {
        seen.add(model)
        result.push(model)
      }
    })
  })
  return result.sort((a, b) => a.localeCompare(b))
}

export function collectSchedulableModels(channels) {
  return collectModels(channels, { enabledOnly: true })
    .filter(model => channels.some(channel => isModelSchedulable(channel, model)))
}

export function providerForModel(model) {
  const id = String(model || '').toLowerCase()
  if (id.startsWith('gpt-') || id.startsWith('o1') || id.startsWith('o3') || id.startsWith('o4') || id.includes('openai')) return 'OpenAI'
  if (id.startsWith('claude') || id.includes('anthropic')) return 'Anthropic'
  if (id.includes('gemini') || id.includes('google')) return 'Google'
  if (id.includes('deepseek')) return 'DeepSeek'
  if (id.includes('mimo') || id.includes('xiaomi')) return 'Xiaomi'
  if (id.includes('kimi') || id.includes('moonshot')) return 'Moonshot'
  if (id.includes('qwen') || id.includes('qwq') || id.includes('dashscope')) return 'Alibaba'
  if (id.includes('glm') || id.includes('chatglm') || id.includes('codegeex')) return 'Zhipu'
  if (id.includes('grok')) return 'xAI'
  if (id.includes('command') || id.includes('cohere')) return 'Cohere'
  if (id.includes('llama') || id.includes('meta')) return 'Meta'
  return 'Other'
}

export function groupModelsByProvider(models) {
  const groups = new Map()
  models.forEach(model => {
    const provider = providerForModel(model)
    if (!groups.has(provider)) groups.set(provider, [])
    groups.get(provider).push(model)
  })
  return Array.from(groups.entries())
    .map(([provider, items]) => ({
      provider,
      models: items.sort((a, b) => a.localeCompare(b)),
    }))
    .sort((a, b) => {
      if (a.provider === 'Other') return 1
      if (b.provider === 'Other') return -1
      return a.provider.localeCompare(b.provider)
    })
}
