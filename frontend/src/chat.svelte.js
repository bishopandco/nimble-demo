export const MESSAGE_LIMIT = 100
export const TEXT_LIMIT = 500
export const NAME_LIMIT = 40
export const characterCount = (text) => Array.from(text).length

function chatEntry(payload) {
  if (payload?.type !== 'chat' || !Number.isSafeInteger(payload.id) || payload.id <= 0 ||
      typeof payload.name !== 'string' || typeof payload.message !== 'string' ||
      !Number.isSafeInteger(payload.client_id) || payload.client_id <= 0) return null
  const date = new Date(payload.timestamp)
  if (Number.isNaN(date.getTime())) return null
  return { id: payload.id, name: payload.name, text: payload.message, clientID: payload.client_id, date, provider: ['codex', 'claude'].includes(payload.provider) ? payload.provider : '', replyTo: Number.isSafeInteger(payload.reply_to) ? payload.reply_to : 0 }
}

export function createChatSocket() {
  const url = new URL('/ws', window.location.href)
  url.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const endpoint = url.href
  const state = $state({ status: 'connecting', messages: [], clientID: 0, retryDelay: 0, error: '', botJobs: [], botErrors: {} })
  let socket = null
  let retryTimer = null
  let connectTimer = null
  let retries = 0
  let enabled = true

  function botError(promptID, message) {
    const previous = Object.entries(state.botErrors).filter(([id]) => Number(id) !== promptID)
    state.botErrors = Object.fromEntries([...previous, [promptID, message]].slice(-MESSAGE_LIMIT))
  }

  function clearBotError(promptID) {
    if (state.botErrors[promptID]) {
      state.botErrors = Object.fromEntries(Object.entries(state.botErrors).filter(([id]) => Number(id) !== promptID))
    }
  }

  function release() {
    clearTimeout(retryTimer)
    clearTimeout(connectTimer)
    if (socket) {
      socket.onopen = socket.onclose = socket.onerror = socket.onmessage = null
      socket.close()
      socket = null
    }
  }

  function retry() {
    if (!enabled) return
    state.status = 'reconnecting'
    state.retryDelay = Math.min(1000 * 2 ** Math.min(retries++, 4), 10_000)
    retryTimer = setTimeout(connect, state.retryDelay)
  }

  function connect() {
    release()
    enabled = true
    state.status = 'connecting'
    state.error = ''
    let connection
    try {
      connection = new WebSocket(endpoint)
    } catch {
      state.error = 'Could not open the connection.'
      retry()
      return
    }
    socket = connection
    connectTimer = setTimeout(() => {
      if (socket !== connection) return
      release()
      state.error = 'The connection timed out.'
      retry()
    }, 10_000)

    connection.onopen = () => {
      if (socket !== connection) return
      clearTimeout(connectTimer)
      retries = 0
      state.retryDelay = 0
      state.status = 'connected'
      state.error = ''
    }
    connection.onmessage = ({ data }) => {
      if (socket !== connection) return
      let payload
      try { payload = JSON.parse(data) } catch { return }
      if (payload?.type === 'ready') {
        state.clientID = payload.client_id || 0
        // Replace the list with the shared snapshot after every connection.
        const history = (Array.isArray(payload.history) ? payload.history : [])
          .map(chatEntry).filter(Boolean).slice(-MESSAGE_LIMIT)
        const pending = Array.isArray(payload.pending_bot_ids)
          ? [...new Set(payload.pending_bot_ids.filter((id) => Number.isSafeInteger(id) && id > 0))] : []
        const completed = new Set(history.filter((entry) => entry.provider).map((entry) => entry.replyTo))
        for (const id of state.botJobs) {
          if (!pending.includes(id) && !completed.has(id)) {
            botError(id, 'This request ended while disconnected. Its result is unavailable.')
          }
        }
        for (const id of completed) clearBotError(id)
        state.messages = history
        state.botJobs = pending
      } else if (payload?.type === 'chat') {
        const entry = chatEntry(payload)
        if (entry && !state.messages.some((item) => item.id === entry.id)) {
          state.messages = [...state.messages, entry].slice(-MESSAGE_LIMIT)
        }
        if (entry?.provider && entry.replyTo > 0) {
          state.botJobs = state.botJobs.filter((id) => id !== entry.replyTo)
          clearBotError(entry.replyTo)
        }
      } else if (payload?.type === 'bot_status' && Number.isSafeInteger(payload.id) && payload.id > 0) {
        if (['thinking', 'queued', 'running'].includes(payload.state)) {
          if (!state.botJobs.includes(payload.id)) state.botJobs = [...state.botJobs, payload.id]
          clearBotError(payload.id)
        } else if (payload.state === 'done' || payload.state === 'failed') {
          state.botJobs = state.botJobs.filter((id) => id !== payload.id)
          if (payload.state === 'failed' && !state.botErrors[payload.id]) {
            botError(payload.id, 'The bot could not complete this request.')
          } else if (payload.state === 'done' && !state.messages.some((entry) => entry.provider && entry.replyTo === payload.id)) {
            botError(payload.id, 'The bot finished without a visible response.')
          }
        }
      } else if (payload?.type === 'error') {
        const message = typeof payload.message === 'string' ? payload.message : 'The message was rejected.'
        if (Number.isSafeInteger(payload.reply_to) && payload.reply_to > 0) {
          botError(payload.reply_to, message)
          state.botJobs = state.botJobs.filter((id) => id !== payload.reply_to)
        } else state.error = message
      }
    }
    connection.onerror = () => {
      if (socket === connection) state.error = 'Could not reach the chatroom.'
    }
    connection.onclose = () => {
      if (socket !== connection) return
      release()
      retry()
    }
  }

  function disconnect() {
    enabled = false
    release()
    retries = 0
    state.retryDelay = 0
    state.status = 'disconnected'
    state.error = ''
  }

  function sendChat(name, message) {
    const trimmedName = name.trim()
    const trimmedMessage = message.trim()
    if (!socket || socket.readyState !== WebSocket.OPEN) return false
    if (!trimmedMessage || characterCount(trimmedMessage) > TEXT_LIMIT || characterCount(trimmedName) > NAME_LIMIT) return false
    try {
      socket.send(JSON.stringify({ type: 'chat', name: trimmedName, message: trimmedMessage }))
      state.error = ''
      return true
    } catch {
      state.error = 'The message could not be sent. Your draft is still here.'
      return false
    }
  }

  return { endpoint, state, connect, disconnect, sendChat }
}
