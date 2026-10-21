<script>
  import { onMount, untrack } from 'svelte'
  import MeasuredText from './MeasuredText.svelte'
  import MessageBody from './MessageBody.svelte'
  import BotMonitor from './BotMonitor.svelte'
  import { buildThread } from './thread.js'
  import { characterCount, MESSAGE_LIMIT, NAME_LIMIT, TEXT_LIMIT, createChatSocket } from './chat.svelte.js'

  const chat = createChatSocket()
  const connection = chat.state
  const thread = $derived(buildThread(connection.messages, connection.botJobs, connection.botErrors))
  let name = $state('')
  let draft = $state('')
  let conversation = $state(null)
  let composer = $state(null)
  let followLatest = $state(true)
  let monitorOpen = $state(false)
  const labels = { connecting: 'Connecting', connected: 'Connected', reconnecting: 'Reconnecting', disconnected: 'Disconnected' }
  const guestName = $derived(connection.clientID ? `Guest ${connection.clientID}` : 'Guest')
  const draftLength = $derived(characterCount(draft))
  const nameLength = $derived(characterCount(name.trim()))
  const canSend = $derived(connection.status === 'connected' && draft.trim() && draftLength <= TEXT_LIMIT && nameLength <= NAME_LIMIT)
  const description = $derived.by(() => {
    if (connection.status === 'connected') return 'You’re in the shared room.'
    if (connection.status === 'reconnecting') return `Rejoining in ${connection.retryDelay / 1000} seconds.`
    if (connection.status === 'connecting') return 'Joining the conversation…'
    return 'You’re offline. Connect to rejoin.'
  })
  const time = (date) => date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

  function submit(event) {
    event?.preventDefault()
    if (!canSend || !chat.sendChat(name, draft)) return
    draft = ''
    followLatest = true
    composer?.focus()
  }

  function onKeydown(event) {
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
      event.preventDefault()
      submit()
    }
  }

  function onScroll() {
    followLatest = conversation.scrollHeight - conversation.scrollTop - conversation.clientHeight < 60
  }

  $effect(() => {
    thread
    untrack(() => {
      if (conversation && followLatest) conversation.scrollTop = conversation.scrollHeight
    })
  })

  onMount(() => {
    chat.connect()
    return chat.disconnect
  })
</script>

<div class="shell">
  <header class="topbar grid">
    <a class="brand" href="/" aria-label="Chatroom home">Chatroom</a>
    <div class="connection-status {connection.status}" role="status"><span class="status-dot" aria-hidden="true"></span>{labels[connection.status]}</div>
  </header>
  <main>
    <div class="page-heading grid">
      <h1>Go API / Svelte Socket Demo</h1>
    </div>
    <div class="workspace grid">
      <aside class="profile-panel" aria-labelledby="profile-heading">
        <h2 id="profile-heading">Join the room</h2>
        <label for="display-name">Display name</label>
        <input id="display-name" bind:value={name} maxlength={NAME_LIMIT * 2} placeholder={guestName} aria-invalid={nameLength > NAME_LIMIT} aria-describedby="name-hint" autocomplete="nickname" />
        <p id="name-hint" class="field-hint" class:invalid={nameLength > NAME_LIMIT} aria-live="polite">{#if nameLength > NAME_LIMIT}Use {NAME_LIMIT} characters or fewer.{:else}Leave blank to chat as {guestName}.{/if}</p>
        <p class="connection-description" aria-live="polite">{description}</p>
        {#if connection.status === 'disconnected'}
          <button class="button" onclick={chat.connect}>Connect to room <span aria-hidden="true">↗</span></button>
        {:else}
          <button class="button" onclick={chat.disconnect}>Disconnect <span aria-hidden="true">↗</span></button>
        {/if}
        <p class="room-note">Open another window to join as someone else.</p>
      </aside>
      <section class="chat-panel" aria-labelledby="chat-heading">
        <div class="chat-toolbar">
          <h2 id="chat-heading"><span class="channel-hash" aria-hidden="true">#</span>general</h2>
        </div>
        <div bind:this={conversation} class="conversation" role="log" aria-label="Chat messages" aria-live="polite" aria-relevant="additions text" onscroll={onScroll}>
          {#if thread.length === 0}
            <div class="empty-state">
              <h3>A conversation<br />starts here.</h3>
              <p>Say hello to everyone in the room.</p>
            </div>
          {:else}
            <ol class="chat-messages">
              {#each thread as entry (entry.key)}
                <li class="chat-message" class:own={entry.clientID === connection.clientID} class:bot-message={entry.bot}>
                  <div class="message-content">
                    <div class="message-meta"><strong><MeasuredText text={entry.name} />{#if entry.clientID === connection.clientID}<span class="you-label">you</span>{/if}{#if entry.bot && entry.name !== 'Bot'}<span class="you-label">bot</span>{/if}</strong>{#if entry.date}<time datetime={entry.date.toISOString()}>{time(entry.date)}</time>{/if}</div>
                    {#if entry.thinking}
                      <div class="message-bubble bot-activity" role="status" aria-live="polite"><span class="progress-spinner" aria-hidden="true"></span><span>Thinking…</span></div>
                    {:else if entry.error}
                      <div class="message-bubble chat-error" role="alert"><MeasuredText text={entry.text} /></div>
                    {:else}
                      <div class="message-bubble"><MessageBody text={entry.text} /></div>
                    {/if}
                  </div>
                </li>
              {/each}
            </ol>
          {/if}
        </div>
        <div class="composer-area">
          {#if connection.error}<p class="chat-error" role="alert"><MeasuredText text={connection.error} /></p>{/if}
          <form class="composer" onsubmit={submit}>
            <label class="sr-only" for="chat-message">Message</label>
            <textarea id="chat-message" bind:this={composer} bind:value={draft} maxlength={TEXT_LIMIT * 2} disabled={connection.status !== 'connected'} placeholder={connection.status === 'connected' ? 'Message everyone…' : 'Connect to send a message…'} rows="2" onkeydown={onKeydown}></textarea>
            <div class="composer-toolbar"><span class="composer-hint">Enter to send.<br />Shift + Enter for a new line.<br />/bot followed by a prompt to ask.</span><div class="send-controls"><span class="character-count" class:over={draftLength > TEXT_LIMIT}>{draftLength}/{TEXT_LIMIT}</span><button class="send-button" type="submit" disabled={!canSend}>Send <span aria-hidden="true">↑</span></button></div></div>
          </form>
        </div>
        <footer class="chat-footer"><p>The latest {MESSAGE_LIMIT} messages stay in this room.<br />Messages stay saved after the server restarts.</p></footer>
      </section>
      <details class="monitor-panel" ontoggle={(event) => { monitorOpen = event.currentTarget.open }}>
        <summary>Bot monitor</summary>
        {#if monitorOpen}<BotMonitor />{/if}
      </details>
    </div>
  </main>
</div>
