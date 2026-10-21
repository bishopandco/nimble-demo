<script>
  import { onMount } from 'svelte'
  import MeasuredText from './MeasuredText.svelte'

  let connection = $state('Connecting')
  let source = $state('starting')
  let calls = $state([])
  let events = $state([])
  let error = $state('')
  let now = $state(Date.now())
  const states = { queued: 'Queued', running: 'Running', completed: 'Completed', failed: 'Failed', canceled: 'Canceled', interrupted: 'Interrupted' }
  const eventNames = { queued: 'Queued', started: 'Started', process_started: 'Process started', sample: 'Sample', completed: 'Completed', failed: 'Failed', canceled: 'Canceled', interrupted: 'Interrupted' }
  const memory = (value) => typeof value === 'number' ? `${(value / 1048576).toFixed(1)} MB` : '—'
  const cpu = (value) => typeof value === 'number' ? `${value.toFixed(1)}%` : '—'
  const timestamp = (value) => new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  function duration(call) {
    if (!call.started_at) return '—'
    const end = call.finished_at ? new Date(call.finished_at).getTime() : now
    const seconds = Math.max(0, Math.floor((end - new Date(call.started_at).getTime()) / 1000))
    return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`
  }

  onMount(() => {
    const url = new URL('/ws/monitor', window.location.href)
    url.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    let socket
    let retryTimer
    let handshakeTimer
    let retries = 0
    let enabled = true
    const clock = setInterval(() => { now = Date.now() }, 1000)
    function release() {
      clearTimeout(handshakeTimer)
      if (socket) {
        socket.onopen = socket.onmessage = socket.onclose = socket.onerror = null
        socket.close()
        socket = null
      }
    }
    function retry() {
      release()
      if (!enabled) return
      connection = 'Reconnecting'
      retryTimer = setTimeout(connect, Math.min(1000 * 2 ** Math.min(retries++, 4), 10000))
    }
    function connect() {
      if (!enabled) return
      connection = retries ? 'Reconnecting' : 'Connecting'
      try { socket = new WebSocket(url.href) } catch { retry(); return }
      handshakeTimer = setTimeout(retry, 10000)
      socket.onopen = () => {
        clearTimeout(handshakeTimer)
        connection = 'Connected'
        retries = 0
      }
      socket.onmessage = ({ data }) => {
        let payload
        try { payload = JSON.parse(data) } catch { return }
        if (payload?.type !== 'monitor_snapshot') return
        calls = Array.isArray(payload.calls) ? payload.calls : []
        events = Array.isArray(payload.events) ? payload.events : []
        source = payload.source || 'starting'
        error = payload.error || ''
        now = Date.now()
      }
      socket.onerror = () => { error = 'The monitor socket is unavailable.' }
      socket.onclose = retry
    }
    connect()
    return () => {
      enabled = false
      clearInterval(clock)
      clearTimeout(retryTimer)
      release()
    }
  })
</script>

<div class="bot-monitor">
  <p class="monitor-status" aria-live="polite">{connection}{#if connection === 'Connected'} · {source === 'live' ? 'Live samples' : source === 'offline' ? 'Sampler offline' : 'Starting sampler'}{/if}</p>
  {#if error}<p class="chat-error" role="alert"><MeasuredText text={error} /></p>{/if}
  {#if calls.length === 0}
    <p>No bot calls recorded yet.</p>
  {:else}
    <div class="monitor-table-wrap" role="region" tabindex="0" aria-label="Bot call instances">
      <table class="monitor-table">
        <caption class="sr-only">Bot calls and their latest process statistics</caption>
        <thead><tr><th scope="col">Call</th><th scope="col">Bot</th><th scope="col">State</th><th scope="col">PID</th><th scope="col">Runtime</th><th scope="col">Memory</th><th scope="col">CPU</th></tr></thead>
        <tbody>
          {#each calls as call (call.id)}
            <tr>
              <td>#{call.id}</td><td>{call.provider}</td><td class:monitor-failed={['failed', 'interrupted'].includes(call.status)}>{states[call.status] || call.status}</td>
              <td class="monitor-pids">CLI {call.provider_pid || '—'}<br />Sidecar {call.pid || '—'}</td>
              <td>{duration(call)}</td><td>{memory(call.memory_bytes)}</td><td>{cpu(call.cpu_percent)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  <p class="monitor-note">CPU and memory include the sidecar and its children.<br />A dash means no process sample is available.</p>
  <h3>Recent events</h3>
  {#if events.length === 0}
    <p>No events recorded yet.</p>
  {:else}
    <ol class="monitor-events" aria-label="Bot event history">
      {#each events as event (event.id)}
        <li>
          <div><span>#{event.call_id} · {eventNames[event.kind] || event.kind}</span><time datetime={event.timestamp}>{timestamp(event.timestamp)}</time></div>
          {#if event.kind === 'sample'}<p>{memory(event.call.memory_bytes)} · {cpu(event.call.cpu_percent)} · {event.call.process_count} processes</p>{/if}
          {#if event.call.error}<p class="monitor-failed"><MeasuredText text={event.call.error} /></p>{/if}
        </li>
      {/each}
    </ol>
  {/if}
</div>
