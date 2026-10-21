// Reserve one keyed row per bot request, then replace its thinking content.
export function buildThread(messages, pendingIDs, errors = {}) {
  const replies = new Map()
  for (const entry of messages) {
    if (!entry.provider || entry.replyTo <= 0) continue
    const reply = replies.get(entry.replyTo)
    if (reply) reply.text += entry.text
    else replies.set(entry.replyTo, { ...entry })
  }

  const pending = new Set(pendingIDs)
  const shown = new Set()
  const thread = []
  function appendBot(promptID, date = null) {
    if (shown.has(promptID)) return
    const reply = replies.get(promptID)
    const error = errors[promptID]
    if (!reply && !error && !pending.has(promptID)) return
    shown.add(promptID)
    thread.push({
      key: `bot:${promptID}`, bot: true, thinking: !reply && !error,
      name: reply?.name || 'Bot', clientID: reply?.clientID || 0,
      date: reply?.date || date, text: reply?.text || error || '', error: !reply && Boolean(error),
    })
  }

  const promptIDs = new Set(messages.filter((entry) => !entry.provider).map((entry) => entry.id))
  for (const entry of messages) {
    if (entry.provider && entry.replyTo > 0) {
      // The prompt may have fallen outside the history window.
      if (!promptIDs.has(entry.replyTo)) appendBot(entry.replyTo, entry.date)
    } else {
      thread.push({ ...entry, key: `message:${entry.id}`, bot: Boolean(entry.provider) })
      appendBot(entry.id, entry.date)
    }
  }
  for (const id of pendingIDs) appendBot(id)
  for (const id of Object.keys(errors)) appendBot(Number(id))
  return thread
}

// Keep fenced code literal and preserve indentation, newlines, and language tags.
export function messageBlocks(text) {
  const blocks = []
  let prose = ''
  let code = ''
  let fence = null
  for (const line of text.match(/[^\n]*\n|[^\n]+$/gu) || []) {
    const content = line.replace(/\r?\n$/u, '')
    if (fence) {
      const closing = /^ {0,3}(`{3,}|~{3,})[\t ]*$/u.exec(content)
      if (closing && closing[1][0] === fence.marker && closing[1].length >= fence.length) {
        blocks.push({ type: 'code', text: code, language: fence.language })
        fence = null
        code = ''
      } else code += line
      continue
    }
    const opening = /^ {0,3}(`{3,}|~{3,})([^\r\n]*)\r?$/u.exec(content)
    if (opening && !(opening[1][0] === '`' && opening[2].includes('`'))) {
      if (prose) blocks.push({ type: 'text', text: prose })
      prose = ''
      const language = opening[2].trim().split(/\s+/u)[0]
      fence = { marker: opening[1][0], length: opening[1].length, language: /^[\w.+#-]{1,32}$/u.test(language) ? language : '' }
    } else prose += line
  }
  if (fence) blocks.push({ type: 'code', text: code, language: fence.language })
  if (prose) blocks.push({ type: 'text', text: prose })
  return blocks
}
