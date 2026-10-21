import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { tick } from 'svelte'
import App from './App.svelte'

class MockWebSocket {
  static OPEN = 1
  static instances = []
  constructor(url) {
    this.url = url
    this.readyState = 0
    this.send = vi.fn()
    this.close = vi.fn(() => { this.readyState = 3 })
    MockWebSocket.instances.push(this)
  }
  open() { this.readyState = 1; this.onopen?.() }
  receive(data) { this.onmessage?.({ data: typeof data === 'string' ? data : JSON.stringify(data) }) }
  drop() { this.readyState = 3; this.onclose?.() }
}

const chat = (id, fields = {}) => ({
  type: 'chat', id, client_id: 7, name: 'Ada', message: `Hello ${id}`,
  timestamp: '2026-10-21T15:00:00Z', ...fields,
})

describe('shared chatroom', () => {
  let view
  const element = (selector) => view.container.querySelector(selector)
  const all = (selector) => Array.from(view.container.querySelectorAll(selector))
  const socket = () => MockWebSocket.instances.at(-1)
  const send = () => element('button[type="submit"]')
  const open = async (history = []) => {
    socket().open()
    socket().receive({ type: 'ready', client_id: 7, history })
    await tick()
  }
  beforeEach(async () => {
    vi.useFakeTimers()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    view = render(App)
    await tick()
  })
  afterEach(async () => {
    await cleanup()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('joins the same-origin room and loads shared history in order', async () => {
    expect(new URL(socket().url).pathname).toBe('/ws')
    expect(new URL(socket().url).host).toBe(window.location.host)
    expect(element('[role="status"]').textContent).toBe('Connecting')
    expect(element('textarea').getAttribute('disabled')).not.toBeNull()
    await open([chat(1, { client_id: 8, name: 'Grace' }), chat(2)])
    expect(element('[role="status"]').textContent).toBe('Connected')
    expect(all('.message-bubble').map((item) => item.textContent)).toEqual(['Hello 1', 'Hello 2'])
    expect(all('.chat-message.own')).toHaveLength(1)
    expect(element('#display-name').getAttribute('placeholder')).toBe('Guest 7')
  })

  it('sends a message and displays it only after the shared server echo', async () => {
    await open()
    await fireEvent.input(element('#display-name'), { target: { value: ' Ada ' } })
    await fireEvent.input(element('textarea'), { target: { value: ' Hello everyone! ' } })
    await fireEvent.submit(element('form'))
    expect(socket().send).toHaveBeenCalledWith(JSON.stringify({ type: 'chat', name: 'Ada', message: 'Hello everyone!' }))
    expect(element('textarea').value).toBe('')
    expect(all('.chat-message')).toHaveLength(0)
    socket().receive(chat(1, { message: 'Hello everyone!' }))
    await tick()
    expect(element('.message-bubble').textContent).toBe('Hello everyone!')
    expect(element('.you-label').textContent).toBe('you')
  })

  it('sends bot commands and tracks queued requests without duplicating jobs', async () => {
    await open()
    await fireEvent.input(element('textarea'), { target: { value: '/bot claude Explain sockets' } })
    await fireEvent.submit(element('form'))
    expect(JSON.parse(socket().send.mock.calls[0][0]).message).toBe('/bot claude Explain sockets')
    socket().receive({ type: 'bot_status', id: 1, state: 'thinking' })
    socket().receive({ type: 'bot_status', id: 1, state: 'running' })
    socket().receive({ type: 'bot_status', id: 2, state: 'thinking' })
    await tick()
    expect(all('.bot-activity')).toHaveLength(2)
    expect(all('.progress-spinner')).toHaveLength(2)
    const placeholder = element('.bot-message')
    socket().receive(chat(3, { name: 'Claude', client_id: 8, provider: 'claude', reply_to: 1, message: '<b>Plain reply</b>' }))
    socket().receive({ type: 'bot_status', id: 1, state: 'done' })
    await tick()
    expect(element('.message-bubble').textContent).toBe('<b>Plain reply</b>')
    expect(element('.you-label').textContent).toBe('bot')
    expect(element('.bot-message')).toBe(placeholder)
    expect(placeholder.querySelector('.progress-spinner')).toBeNull()
    expect(all('.bot-activity')).toHaveLength(1)
    socket().receive({ type: 'bot_status', id: 2, state: 'failed' })
    await tick()
    expect(element('.bot-activity')).toBeNull()
  })

  it('replaces the thinking row in place and renders code across reply chunks', async () => {
    await open([chat(1, { message: '/bot Show a code example' })])
    socket().receive({ type: 'bot_status', id: 1, state: 'thinking' })
    await tick()
    const placeholder = element('.bot-message')
    socket().receive(chat(2, { client_id: 8, name: 'Grace', message: 'Still chatting' }))
    socket().receive(chat(3, { client_id: 9, name: 'Codex', provider: 'codex', reply_to: 1, message: 'An example:\n```js\n  const html = "<img ' }))
    socket().receive(chat(4, { client_id: 9, name: 'Codex', provider: 'codex', reply_to: 1, message: 'src=x>";\n  console.log(html);\n```\nDone.' }))
    await tick()
    expect(element('.bot-message')).toBe(placeholder)
    expect(all('.chat-message')).toHaveLength(3)
    expect(all('pre')).toHaveLength(1)
    expect(element('pre code').textContent).toBe('  const html = "<img src=x>";\n  console.log(html);\n')
    expect(element('pre code').className).toBe('language-js')
    expect(all('img')).toHaveLength(0)
    expect(element('.progress-spinner')).toBeNull()
  })

  it('supports Enter to send, Shift+Enter for new lines, and IME composition', async () => {
    await open()
    const input = element('textarea')
    await fireEvent.input(input, { target: { value: 'Hello' } })
    await fireEvent.keyDown(input, { key: 'Enter', shiftKey: true })
    await fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
    expect(socket().send).not.toHaveBeenCalled()
    await fireEvent.keyDown(input, { key: 'Enter' })
    expect(socket().send).toHaveBeenCalledOnce()
    expect(JSON.parse(socket().send.mock.calls[0][0])).toEqual({ type: 'chat', name: '', message: 'Hello' })
  })

  it('blocks blank, long, and offline messages', async () => {
    await open()
    expect(send().getAttribute('disabled')).not.toBeNull()
    await fireEvent.input(element('textarea'), { target: { value: '  ' } })
    await fireEvent.submit(element('form'))
    await fireEvent.input(element('textarea'), { target: { value: 'x'.repeat(501) } })
    await fireEvent.submit(element('form'))
    expect(send().getAttribute('disabled')).not.toBeNull()
    expect(element('.character-count').textContent).toBe('501/500')
    await fireEvent.input(element('textarea'), { target: { value: 'Hello' } })
    await fireEvent.input(element('#display-name'), { target: { value: '🌍'.repeat(41) } })
    await fireEvent.submit(element('form'))
    expect(element('#display-name').getAttribute('aria-invalid')).toBe('true')
    expect(send().getAttribute('disabled')).not.toBeNull()
    await fireEvent.input(element('#display-name'), { target: { value: '' } })
    await fireEvent.click(element('.profile-panel button'))
    await fireEvent.submit(element('form'))
    expect(socket().send).not.toHaveBeenCalled()
    expect(element('textarea').value).toBe('Hello')
  })

  it('counts Unicode characters and accepts a 500-character draft', async () => {
    await open()
    await fireEvent.input(element('#display-name'), { target: { value: '🌍'.repeat(40) } })
    await fireEvent.input(element('textarea'), { target: { value: '🌍'.repeat(500) } })
    expect(element('.character-count').textContent).toBe('500/500')
    expect(send().getAttribute('disabled')).toBeNull()
    await fireEvent.submit(element('form'))
    expect(Array.from(JSON.parse(socket().send.mock.calls[0][0]).message)).toHaveLength(500)
    expect(Array.from(JSON.parse(socket().send.mock.calls[0][0]).name)).toHaveLength(40)
  })

  it('keeps the draft if sending fails and displays server errors', async () => {
    await open()
    socket().send.mockImplementationOnce(() => { throw new Error('closed') })
    await fireEvent.input(element('textarea'), { target: { value: 'Keep this draft' } })
    await fireEvent.submit(element('form'))
    expect(element('textarea').value).toBe('Keep this draft')
    expect(element('[role="alert"]').textContent).toContain('could not be sent')
    socket().receive({ type: 'error', message: 'Rejected by the server' })
    await tick()
    expect(element('[role="alert"]').textContent).toBe('Rejected by the server')
  })

  it('renders names and messages as text and ignores non-chat events', async () => {
    await open()
    socket().receive('not json')
    socket().receive('null')
    socket().receive({ type: 'unknown', message: 'Ignore this event' })
    socket().receive({ type: 'pong' })
    socket().receive(chat(1, { name: '<img src=x>', message: '<script>alert(1)</script>' }))
    await tick()
    expect(all('.chat-message')).toHaveLength(1)
    expect(all('img, script')).toHaveLength(0)
    expect(element('.message-bubble').textContent).toBe('<script>alert(1)</script>')
    expect(view.container.textContent).toContain('<img src=x>')
  })

  it('retains the latest 100 messages and ignores duplicate delivery', async () => {
    await open()
    for (let id = 1; id <= 105; id++) socket().receive(chat(id))
    socket().receive(chat(105))
    await tick()
    const messages = all('.message-bubble')
    expect(messages).toHaveLength(100)
    expect(messages[0].textContent).toBe('Hello 6')
    expect(messages[99].textContent).toBe('Hello 105')
  })

  it('limits displayed lines to ten words without changing message whitespace', async () => {
    await open()
    const message = 'one two three four five six seven eight nine ten eleven twelve\nshort line\n  one two three four five six seven eight nine ten eleven'
    socket().receive(chat(1, { message }))
    await tick()
    const bubble = element('.message-bubble')
    expect(bubble.textContent).toBe(message)
    expect(bubble.querySelectorAll('br')).toHaveLength(2)
    const displayed = Array.from(bubble.childNodes)
      .map((node) => node.nodeName === 'BR' ? '\n' : node.nodeType === 3 ? node.textContent : '').join('')
    const lines = displayed.split(/\n/u)
    expect(lines.every((line) => line.trim().split(/\s+/u).length <= 10)).toBe(true)
  })

  it('rejoins and replaces local messages with the shared history snapshot', async () => {
    await open([chat(1)])
    socket().drop()
    await tick()
    expect(element('[role="status"]').textContent).toBe('Reconnecting')
    await vi.advanceTimersByTimeAsync(1000)
    expect(MockWebSocket.instances).toHaveLength(2)
    socket().open()
    socket().receive({ type: 'ready', client_id: 9, history: [chat(1), chat(2)] })
    await tick()
    expect(all('.message-bubble').map((item) => item.textContent)).toEqual(['Hello 1', 'Hello 2'])
    socket().receive({ type: 'ready', client_id: 9, history: [] })
    await tick()
    expect(all('.chat-message')).toHaveLength(0)
  })

  it('backs off failed connections up to ten seconds', async () => {
    for (const delay of [1000, 2000, 4000, 8000, 10000, 10000]) {
      const count = MockWebSocket.instances.length
      socket().drop()
      await vi.advanceTimersByTimeAsync(delay - 1)
      expect(MockWebSocket.instances).toHaveLength(count)
      await vi.advanceTimersByTimeAsync(1)
      expect(MockWebSocket.instances).toHaveLength(count + 1)
    }
  })

  it('pauses retries and ignores old callbacks after manual disconnection', async () => {
    await open()
    const previous = socket()
    const staleMessage = previous.onmessage
    await fireEvent.click(element('.profile-panel button'))
    staleMessage({ data: JSON.stringify(chat(1)) })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(previous.close).toHaveBeenCalled()
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(element('[role="status"]').textContent).toBe('Disconnected')
    expect(all('.chat-message')).toHaveLength(0)
    await fireEvent.click(element('.profile-panel button'))
    expect(MockWebSocket.instances).toHaveLength(2)
  })

  it('times out stalled handshakes and retries', async () => {
    const stalled = socket()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(stalled.close).toHaveBeenCalled()
    expect(element('[role="status"]').textContent).toBe('Reconnecting')
    expect(element('[role="alert"]').textContent).toContain('timed out')
    await vi.advanceTimersByTimeAsync(1000)
    expect(MockWebSocket.instances).toHaveLength(2)
  })

  it('closes active connections and cancels retries on unmount', async () => {
    const connection = socket()
    await view.unmount()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(connection.close).toHaveBeenCalled()
    expect(MockWebSocket.instances).toHaveLength(1)
  })
})
