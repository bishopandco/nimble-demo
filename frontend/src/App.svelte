<script>
  import { onMount, untrack } from 'svelte'
  import { characterCount, MESSAGE_LIMIT, NAME_LIMIT, TEXT_LIMIT, createChatSocket } from './chat.svelte.js'

  const chat = createChatSocket()
  const connection = chat.state
  let name = $state('')
  let draft = $state('')
  let conversation = $state(null)
  let composer = $state(null)
  let followLatest = $state(true)
  const labels = { connecting: 'Connecting', connected: 'Connected', reconnecting: 'Reconnecting', disconnected: 'Disconnected' }
  const guestName = $derived(connection.clientID ? `Guest ${connection.clientID}` : 'Guest')
  const draftLength = $derived(characterCount(draft))
  const nameLength = $derived(characterCount(name.trim()))
  const canSend = $derived(connection.status === 'connected' && draft.trim() && draftLength <= TEXT_LIMIT && nameLength <= NAME_LIMIT)
  const description = $derived.by(() => {
    if (connection.status === 'connected') return 'You’re in the shared room.'
    if (connection.status === 'reconnecting') return `Rejoining automatically · ${connection.retryDelay / 1000}s retry delay`
    if (connection.status === 'connecting') return 'Joining the conversation…'
    return 'You’re offline. Connect to rejoin.'
  })
  const time = (date) => date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const initials = (displayName) => Array.from(displayName.trim()).slice(0, 2).join('').toUpperCase()

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
    connection.messages.at(-1)?.id
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
  <header class="topbar">
    <a class="brand" href="/" aria-label="Blog API chat home">
      <span class="brand-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M5 4h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H9l-6 3V6a2 2 0 0 1 2-2Z" /><path d="M8 9h8M8 13h5" /></svg></span>
      Blog API <span class="brand-divider">/</span><span class="brand-section">Chatroom</span>
    </a>
    <span class="protocol">ONE SHARED ROOM</span>
  </header>
  <main>
    <div class="page-heading">
      <div>
        <p class="eyebrow">A PLACE TO TALK</p>
        <h1>The common room<span class="heading-dot">.</span></h1>
        <p class="intro">Chat with everyone connected to the API.</p>
      </div>
      <div class="status-pill {connection.status}" role="status"><span class="status-dot" aria-hidden="true"></span>{labels[connection.status]}</div>
    </div>
    <div class="workspace">
      <aside class="profile-panel panel" aria-labelledby="profile-heading">
        <h2 id="profile-heading">Make yourself at home</h2>
        <label for="display-name">Display name</label>
        <input id="display-name" bind:value={name} maxlength={NAME_LIMIT * 2} placeholder={guestName} aria-invalid={nameLength > NAME_LIMIT} aria-describedby="name-hint" autocomplete="nickname" />
        <p id="name-hint" class="field-hint" class:invalid={nameLength > NAME_LIMIT} aria-live="polite">{#if nameLength > NAME_LIMIT}Use {NAME_LIMIT} characters or fewer.{:else}Leave blank to chat as {guestName}.{/if}</p>
        <div class="room-info">
          <span class="room-icon" aria-hidden="true">#</span>
          <div><strong>general</strong><p>Everyone’s in the same conversation.</p></div>
        </div>
        <p class="connection-description" aria-live="polite">{description}</p>
        {#if connection.status === 'disconnected'}
          <button class="button" onclick={chat.connect}>Connect to room <span aria-hidden="true">↗</span></button>
        {:else}
          <button class="button" onclick={chat.disconnect}>Disconnect</button>
        {/if}
        <div class="room-note"><span class="note-dot" aria-hidden="true"></span><p>Open another tab or window to chat with a second connection.</p></div>
      </aside>
      <section class="chat-panel panel" aria-labelledby="chat-heading">
        <div class="chat-toolbar">
          <div class="chat-title"><span class="channel-hash" aria-hidden="true">#</span><h2 id="chat-heading">general</h2></div>
          <span class="room-label">Shared with everyone</span>
        </div>
        <div bind:this={conversation} class="conversation" role="log" aria-label="Chat messages" aria-live="polite" aria-relevant="additions" onscroll={onScroll}>
          {#if connection.messages.length === 0}
            <div class="empty-state">
              <div class="empty-icon" aria-hidden="true"><svg viewBox="0 0 32 32"><path d="M7 6h18a3 3 0 0 1 3 3v12a3 3 0 0 1-3 3H13l-9 5V9a3 3 0 0 1 3-3Z" /><path d="M10 12h12M10 17h7" /></svg></div>
              <h3>Start the conversation</h3>
              <p>Say hello. Your message will appear<br />for everyone in the room.</p>
            </div>
          {:else}
            <ol class="chat-messages">
              {#each connection.messages as entry (entry.id)}
                <li class="chat-message" class:own={entry.clientID === connection.clientID}>
                  <span class="avatar" aria-hidden="true">{initials(entry.name)}</span>
                  <div class="message-content">
                    <div class="message-meta"><strong>{entry.name}{#if entry.clientID === connection.clientID}<span class="you-label">you</span>{/if}</strong><time datetime={entry.date.toISOString()}>{time(entry.date)}</time></div>
                    <p class="message-bubble">{entry.text}</p>
                  </div>
                </li>
              {/each}
            </ol>
          {/if}
        </div>
        <div class="composer-area">
          {#if connection.error}<p class="chat-error" role="alert">{connection.error}</p>{/if}
          <form class="composer" onsubmit={submit}>
            <label class="sr-only" for="chat-message">Message</label>
            <textarea id="chat-message" bind:this={composer} bind:value={draft} maxlength={TEXT_LIMIT * 2} disabled={connection.status !== 'connected'} placeholder={connection.status === 'connected' ? 'Message everyone…' : 'Connect to send a message…'} rows="2" onkeydown={onKeydown}></textarea>
            <div class="composer-toolbar"><span class="composer-hint">Enter to send <span>·</span> Shift + Enter for a new line</span><div class="send-controls"><span class="character-count" class:over={draftLength > TEXT_LIMIT}>{draftLength}/{TEXT_LIMIT}</span><button class="send-button" type="submit" disabled={!canSend}>Send <span aria-hidden="true">↑</span></button></div></div>
          </form>
        </div>
        <footer class="chat-footer"><span class="footer-dot" aria-hidden="true"></span>Latest {MESSAGE_LIMIT} messages shared with new connections. History resets when the API restarts.</footer>
      </section>
    </div>
    <p class="page-footer">Blog API <span>·</span> One room, every connection</p>
  </main>
</div>
